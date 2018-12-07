// Copyright (c) 2018 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package secret

import (
	"fmt"

	"github.com/gardener/cert-broker/pkg/common"
	"github.com/gardener/cert-broker/pkg/utils"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	extv1beta1 "k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/listers/extensions/v1beta1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

const controllerAgentName = "secret-controller"

var logger *log.Entry

func init() {
	logger = log.WithFields(log.Fields{"APP": controllerAgentName})
}

// Handler holds information about the traget and control cluster.
type Handler struct {
	controlCtx *ControlClusterContext
	targetCtx  *TargetClusterContext
	workqueue  workqueue.RateLimitingInterface
}

// ControlClusterContext holds information about the control cluster.
type ControlClusterContext struct {
	// ResourceNamespace determines the location of the managed secrets.
	ResourceNamespace string
	Client            kubernetes.Interface
	SecretLister      v1.SecretLister
	SecretInformer    cache.SharedIndexInformer
	SecretSync        cache.InformerSynced
}

// TargetClusterContext holds information about the target cluster.
type TargetClusterContext struct {
	Client        kubernetes.Interface
	IngressLister v1beta1.IngressLister
	IngressSync   cache.InformerSynced
}

// NewController creates a new instance of NewController which in turn
// is capable of replicating Secrets.
func NewController(controlCtx *ControlClusterContext, targetCtx *TargetClusterContext) *common.Controller {
	handler := &Handler{
		controlCtx: controlCtx,
		targetCtx:  targetCtx,
		workqueue:  workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Secrets"),
	}
	controlClusterFilter := utils.ControlClusterResourceFilter{Namespace: controlCtx.ResourceNamespace}
	handler.controlCtx.SecretInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controlClusterFilter.FilterMethod(),
		Handler:    &EventHandler{Queue: handler.workqueue},
	})
	return common.NewController(handler, logger)
}

// GetWorkQueue gets the workqueue managed by this handler
func (h *Handler) GetWorkQueue() workqueue.RateLimitingInterface {
	return h.workqueue
}

// GetInformerSyncs gets the informer sync functions to be waited for
// before using the handlers listers
func (h *Handler) GetInformerSyncs() []cache.InformerSynced {
	return []cache.InformerSynced{
		h.controlCtx.SecretSync,
	}
}

// Sync handles actions for the passed reource key
func (h *Handler) Sync(key string) error {
	namespace, secretName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	controlSecret, err := h.controlCtx.SecretLister.Secrets(namespace).Get(secretName)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	targetNamespace, targetSecretName := utils.SplitNamespace(secretName)

	// Check if secret has a Ingress counterpart in target cluster.
	selector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: map[string]string{utils.GardenPurpose: utils.ManagedCert}})
	if err != nil {
		logger.Error("An error occurred while creating the label selector")
		return err
	}
	availableIngresses, err := h.targetCtx.IngressLister.Ingresses(targetNamespace).List(selector)
	if err != nil {
		return err
	}
	exists := hasIngressInTargetCluster(availableIngresses, targetSecretName)
	if !exists {
		logger.Infof("Ignoring Secret %s because no matching Ingress could be found in target cluster", controlSecret.Name)
		return nil
	}

	return h.replicateSecretToTargetCluster(controlSecret)
}

func hasIngressInTargetCluster(ingresses []*extv1beta1.Ingress, secretName string) bool {
	for _, ingress := range ingresses {
		for _, tls := range ingress.Spec.TLS {
			if tls.SecretName == secretName {
				return true
			}
		}
	}
	return false
}

func (h *Handler) replicateSecretToTargetCluster(controlSecret *corev1.Secret) error {
	namespace, name := utils.SplitNamespace(controlSecret.Name)
	targetSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: controlSecret.Data,
	}

	logger.Infof("Creating / Updating secret %s/%s in target cluster", namespace, targetSecret.Name)
	_, err := h.targetCtx.Client.Core().Secrets(namespace).Create(targetSecret)
	if err != nil && apierrors.IsAlreadyExists(err) {
		if _, err := h.targetCtx.Client.CoreV1().Secrets(targetSecret.Namespace).Update(targetSecret); err != nil {
			return err
		}
		return nil
	}
	return err
}

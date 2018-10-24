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

package events

import (
	"fmt"
	"strconv"

	"github.com/gardener/cert-broker/pkg/common"
	"github.com/gardener/cert-broker/pkg/utils"
	log "github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	corev1 "k8s.io/client-go/listers/core/v1"
	lv1beta1 "k8s.io/client-go/listers/extensions/v1beta1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
)

const controllerAgentName = "event-controller"

var logger *log.Entry

func init() {
	logger = log.WithFields(log.Fields{"APP": controllerAgentName})
}

// Handler holds information about the traget and control cluster.
type Handler struct {
	controlCtx *ControlClusterContext
	targetCtx  *TargetClusterContext
	workqueue  workqueue.RateLimitingInterface
	recorder   record.EventRecorder
}

// ControlClusterContext holds information about the control cluster.
type ControlClusterContext struct {
	// ResourceNamespace determines the location of replicated Ingress resources.
	EventInformer      cache.SharedIndexInformer
	EventLister        corev1.EventLister
	EventSync          cache.InformerSynced
	CertificatesLister cache.GenericLister
	CertificatesSync   cache.InformerSynced
	Client             kubernetes.Interface
}

// TargetClusterContext holds information about the target cluster.
type TargetClusterContext struct {
	Client        kubernetes.Interface
	IngressLister lv1beta1.IngressLister
	IngressSync   cache.InformerSynced
}

// NewController creates a new instance of Controller which in turn
// is capable of replicating Ingress resources.
func NewController(controlCtx *ControlClusterContext, targetCtx *TargetClusterContext) *common.Controller {
	handler := &Handler{
		controlCtx: controlCtx,
		targetCtx:  targetCtx,
		workqueue:  workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Events"),
		recorder:   utils.CreateRecorder(targetCtx.Client, logger, "Cert-Broker-Ingress-Control"),
	}
	handler.controlCtx.EventInformer.AddEventHandler(&cache.FilteringResourceEventHandler{
		FilterFunc: certificateIsInvolved,
		Handler:    &EventHandler{Queue: handler.workqueue},
	})
	return common.NewController(handler)
}

func certificateIsInvolved(obj interface{}) bool {
	event, ok := obj.(*v1.Event)
	if !ok {
		return false
	}
	return event.InvolvedObject.APIVersion == utils.CertManagerGroup && event.InvolvedObject.Kind == utils.CertificateKind
}

// GetWorkQueue gets the workqueue managed by this handler
func (h *Handler) GetWorkQueue() workqueue.RateLimitingInterface {
	return h.workqueue
}

// GetInformerSyncs gets the informer sync functions to be waited for
// before using the handlers listers
func (h *Handler) GetInformerSyncs() []cache.InformerSynced {
	return []cache.InformerSynced{
		h.controlCtx.CertificatesSync,
		h.controlCtx.EventSync,
		h.targetCtx.IngressSync,
	}
}

// Sync handles actions for the passed reource key
func (h *Handler) Sync(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}
	event, err := h.controlCtx.EventLister.Events(namespace).Get(name)
	if err != nil {
		return err
	}
	cert, err := h.controlCtx.CertificatesLister.ByNamespace(event.InvolvedObject.Namespace).Get(event.InvolvedObject.Name)
	if err != nil {
		return err
	}
	if cert != nil {
		crt, ok := cert.(*unstructured.Unstructured)
		if ok {
			for _, ownerRef := range crt.GetOwnerReferences() {
				if ownerRef.APIVersion == v1beta1.SchemeGroupVersion.String() && ownerRef.Kind == utils.IngressKind {
					logger.Infof("Replicating event %s to target cluster", event.Name)
					ingNamespace, ingName := utils.SplitNamespace(ownerRef.Name)
					targetIng, err := h.targetCtx.IngressLister.Ingresses(ingNamespace).Get(ingName)
					if err != nil {
						if errors.IsNotFound(err) {
							return nil
						}
						return err
					}
					logger.Infof("Creating event in target cluster for Ingress %s", targetIng.Name)
					h.recorder.Event(targetIng, event.Type, event.Reason, event.Message)

					updatedEvent := event.DeepCopy()
					if updatedEvent.Annotations == nil {
						updatedEvent.Annotations = make(map[string]string)
					}
					updatedEvent.Annotations[utils.GardenCount] = strconv.Itoa(int(event.Count))
					_, err = h.controlCtx.Client.CoreV1().Events(namespace).Update(updatedEvent)
					if err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

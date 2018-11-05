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

package ingress

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/gardener/cert-broker/pkg/common"
	"github.com/gardener/cert-broker/pkg/utils"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	extv1beta1 "k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/listers/core/v1"
	lv1beta1 "k8s.io/client-go/listers/extensions/v1beta1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
)

const controllerAgentName = "ingress-controller"

var logger *log.Entry

func init() {
	logger = log.WithFields(log.Fields{"APP": controllerAgentName})
}

// Handler holds information about the traget and control cluster.
type Handler struct {
	controlCtx          *ControlClusterContext
	targetCtx           *TargetClusterContext
	workqueue           workqueue.RateLimitingInterface
	recorder            record.EventRecorder
	ingressTemplate     *utils.IngressTemplate
	domainToDNSProvider map[string]string
}

// ControlClusterContext holds information about the control cluster.
type ControlClusterContext struct {
	// ResourceNamespace determines the location of replicated Ingress resources.
	ResourceNamespace string
	Client            kubernetes.Interface
	IngressLister     lv1beta1.IngressLister
	IngressSync       cache.InformerSynced
	SecretLister      v1.SecretLister
	SecretSync        cache.InformerSynced
}

// TargetClusterContext holds information about the target cluster.
type TargetClusterContext struct {
	Client          kubernetes.Interface
	IngressInformer cache.SharedIndexInformer
	IngressLister   lv1beta1.IngressLister
	IngressSync     cache.InformerSynced
}

// NewController creates a new instance of Controller which in turn
// is capable of replicating Ingress resources.
func NewController(controlCtx *ControlClusterContext, targetCtx *TargetClusterContext, ingressTemplate *utils.IngressTemplate, domainToDNSProvider map[string]string) *common.Controller {
	handler := &Handler{
		controlCtx:          controlCtx,
		targetCtx:           targetCtx,
		workqueue:           workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Ingresses"),
		recorder:            utils.CreateRecorder(targetCtx.Client, logger, "Cert-Broker-Ingress-Control"),
		ingressTemplate:     ingressTemplate,
		domainToDNSProvider: domainToDNSProvider,
	}
	handler.targetCtx.IngressInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: utils.FilterOnIngressLables,
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
		h.controlCtx.IngressSync,
		h.controlCtx.SecretSync,
		h.targetCtx.IngressSync,
	}
}

// Sync handles actions for the passed reource key
func (h *Handler) Sync(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	controlIngressName := utils.CreateNameForControlCluster(namespace, name)

	// Delete Ingress in control cluster.
	targetIngress, err := h.targetCtx.IngressLister.Ingresses(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			return h.cleanUpIngressAndSecrets(controlIngressName)
		}
		return err
	}

	// Create new Ingress.
	controlIngress, err := h.controlCtx.IngressLister.Ingresses(h.controlCtx.ResourceNamespace).Get(controlIngressName)
	if err != nil {
		if errors.IsNotFound(err) {
			err := h.createIngress(controlIngressName, targetIngress)
			return err
		}
		return err
	}

	// Update Ingress if TLS information has changed.
	return h.updateIngress(targetIngress, controlIngress)
}

func (h *Handler) cleanUpIngressAndSecrets(name string) error {
	ingress, err := h.controlCtx.IngressLister.Ingresses(h.controlCtx.ResourceNamespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return utils.CleanUpIngressAndTLSSecrets(h.controlCtx.Client, ingress, logger)
}

func (h *Handler) createIngress(name string, source *extv1beta1.Ingress) error {
	// Prefix every secret name of the TLS section with the control cluster convention,
	// so we can assign created secret in the control cluster back to target cluster.
	tls, dnsProvider := h.copyTLS(source)
	if len(tls) < 1 {
		return nil
	}
	prefixWithNamespace(tls, source.Namespace)

	controlIngress := h.ingressTemplate.CreateControlIngress(name, h.controlCtx.ResourceNamespace, dnsProvider, tls)

	logger.Infof("Creating Ingress resource %s.", name)
	_, err := h.controlCtx.Client.Extensions().Ingresses(h.controlCtx.ResourceNamespace).Create(controlIngress)
	if err != nil {
		return err
	}
	h.recorder.Event(source, corev1.EventTypeNormal, "Certificate", "Certificate request initiated")
	return nil
}

// An ingress is considered outdated when the TLS information has changed on targetIng.
// The update deletes the Ingress and creates a new one in order to also delete owner referenced objects.
// This is especially important for Certificate CRs managed by Cert-Manager.
func (h *Handler) updateIngress(targetIng, controlIng *extv1beta1.Ingress) error {
	if !deepEqual(targetIng.Spec.TLS, controlIng.Spec.TLS, targetIng.Namespace) {
		updatedIng := controlIng.DeepCopy()
		updatedTLS, dnsProvider := h.copyTLS(targetIng)
		prefixWithNamespace(updatedTLS, targetIng.Namespace)
		updatedIng.Spec.TLS = updatedTLS
		updatedIng.Annotations[utils.DNSProviderKey] = dnsProvider

		logger.Infof("Updating Ingress resource %s.", controlIng.Name)

		_, err := h.controlCtx.Client.Extensions().Ingresses(h.controlCtx.ResourceNamespace).Update(updatedIng)
		if err != nil {
			return err
		}

		h.recorder.Event(targetIng, corev1.EventTypeNormal, "Certificate", "Certificate update requested")
	} else {
		logger.Infof("Ingress resource %s is already synced and up to date.", controlIng.Name)
	}
	return nil
}

func prefixWithNamespace(tls []extv1beta1.IngressTLS, namespace string) {
	for i, element := range tls {
		tls[i].SecretName = utils.CreateNameForControlCluster(namespace, element.SecretName)
	}
}

// Since the TLS secret name in the control cluster is always prefixed by the origin namespace,
// we need to consider this when comparing the TLS sections.
func deepEqual(tls, prefixedTLS []extv1beta1.IngressTLS, namespace string) bool {
	tlsCopy := make([]extv1beta1.IngressTLS, len(tls))
	copy(tlsCopy, tls)
	prefixWithNamespace(tlsCopy, namespace)
	return reflect.DeepEqual(tlsCopy, prefixedTLS)
}

func (h *Handler) copyTLS(ing *extv1beta1.Ingress) ([]extv1beta1.IngressTLS, string) {
	var copiedTLS []extv1beta1.IngressTLS
	var foundProvider string
	for _, tls := range ing.Spec.TLS {
		var copiedHosts []string
		for _, host := range tls.Hosts {
			hostAdded := false
			for managedDomain, provider := range h.domainToDNSProvider {
				if strings.HasSuffix(host, managedDomain) {
					if len(foundProvider) > 0 && foundProvider != provider {
						h.recorder.Event(ing, corev1.EventTypeWarning, "Certificate", "Certificate cannot be requested because configured domains must be managed by exactly one provider.")
						return nil, ""
					}
					foundProvider = provider
					copiedHosts = append(copiedHosts, host)
					hostAdded = true
					break
				}
			}
			if !hostAdded {
				h.recorder.Eventf(ing, corev1.EventTypeWarning, "Certificate", "Domain %s is not managed and thus ignored by Cert-Broker", host)
			}
		}
		if len(copiedHosts) > 0 {
			copiedTLS = append(copiedTLS, extv1beta1.IngressTLS{
				Hosts:      copiedHosts,
				SecretName: tls.SecretName,
			})
		}
	}
	return copiedTLS, foundProvider
}

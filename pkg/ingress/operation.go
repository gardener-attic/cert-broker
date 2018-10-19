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
	"reflect"
	"strings"

	"github.com/gardener/cert-broker/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	extv1beta1 "k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
)

func (c *Controller) cleanUpIngressAndSecrets(name string) error {
	ingress, err := c.controlCtx.IngressLister.Ingresses(c.controlCtx.ResourceNamespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return utils.CleanUpIngressAndTLSSecrets(c.controlCtx.Client, ingress, logger)
}

func (c *Controller) createIngress(name string, source *extv1beta1.Ingress) error {
	// Prefix every secret name of the TLS section with the control cluster convention,
	// so we can assign created secret in the control cluster back to target cluster.
	tls := c.copyTLS(source)
	if len(tls) < 1 {
		return nil
	}
	prefixWithNamespace(tls, source.Namespace)

	controlIngress := c.ingressTemplate.CreateControlIngress(name, c.controlCtx.ResourceNamespace, tls)

	logger.Infof("Creating Ingress resource %s.", name)
	_, err := c.controlCtx.Client.Extensions().Ingresses(c.controlCtx.ResourceNamespace).Create(controlIngress)
	if err != nil {
		return err
	}
	c.recorder.Event(source, corev1.EventTypeNormal, "Certificate", "Certificate request initiated")
	return nil
}

// An ingress is considered outdated when the TLS information has changed on targetIng.
// The update deletes the Ingress and creates a new one in order to also delete owner referenced objects.
// This is especially important for Certificate CRs managed by Cert-Manager.
func (c *Controller) updateIngress(targetIng, controlIng *extv1beta1.Ingress) error {
	if !deepEqual(targetIng.Spec.TLS, controlIng.Spec.TLS, targetIng.Namespace) {
		updatedIng := controlIng.DeepCopy()
		updatedTLS := c.copyTLS(targetIng)
		prefixWithNamespace(updatedTLS, targetIng.Namespace)
		updatedIng.Spec.TLS = updatedTLS

		logger.Infof("Updating Ingress resource %s.", controlIng.Name)

		_, err := c.controlCtx.Client.Extensions().Ingresses(c.controlCtx.ResourceNamespace).Update(updatedIng)
		if err != nil {
			return err
		}

		c.recorder.Event(targetIng, corev1.EventTypeNormal, "Certificate", "Certificate update requested")
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

func (c *Controller) copyTLS(ing *extv1beta1.Ingress) []extv1beta1.IngressTLS {
	var copiedTLS []extv1beta1.IngressTLS
	for _, tls := range ing.Spec.TLS {
		var copiedHosts []string
		for _, host := range tls.Hosts {
			for _, managedDomain := range c.managedDomains {
				if strings.HasSuffix(host, managedDomain) {
					copiedHosts = append(copiedHosts, host)
					break
				}
				c.recorder.Eventf(ing, corev1.EventTypeNormal, "Certificate", "Domain %s is not managed and thus ignored by Cert-Broker", host)
			}
		}
		if len(copiedHosts) > 0 {
			copiedTLS = append(copiedTLS, extv1beta1.IngressTLS{
				Hosts:      copiedHosts,
				SecretName: tls.SecretName,
			})
		}
	}
	return copiedTLS
}

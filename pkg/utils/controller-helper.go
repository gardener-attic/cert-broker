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

package utils

import (
	"math/rand"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
)

// DNSProviderKey is the key used for an Ingress annotation to provider for ACME DNS01 challenge.
const DNSProviderKey = "certmanager.k8s.io/acme-dns01-provider"

var ingressClass string

// CreateIngressAnnotation creates the Cert-Manager annotation section for Ingress resources
type CreateIngressAnnotation func() map[string]string

// IngressTemplate holds the information necessary for Ingress resources managed by Cert-Manager.
type IngressTemplate struct {
	issuerName    string
	challengeType string
	dns01Provider string
}

// CreateIngressTemplate creates an instance of IngressTemplate.
func CreateIngressTemplate(issuerName, challengeType string) *IngressTemplate {
	return &IngressTemplate{
		issuerName:    issuerName,
		challengeType: challengeType,
	}
}

// CreateIngressAnnotations creates annotations turning an Ingress to a Cert-Manager managed resource.
func (t *IngressTemplate) CreateIngressAnnotations() map[string]string {
	annotations := map[string]string{
		"kubernetes.io/ingress.class":            ingressClass,
		"kubernetes.io/tls-acme":                 "true",
		"certmanager.k8s.io/cluster-issuer":      t.issuerName,
		"certmanager.k8s.io/acme-challenge-type": t.challengeType,
	}
	if len(t.dns01Provider) > 0 {
		annotations[DNSProviderKey] = t.dns01Provider
	}
	return annotations
}

// CreateControlIngress creates an Ingress object which includes the given name, namespace and TLS info.
// Further settings which are necessary for the control cluster are set by this method.
func (t *IngressTemplate) CreateControlIngress(name, namespace, dnsProvider string, tls []v1beta1.IngressTLS) *v1beta1.Ingress {
	t.dns01Provider = dnsProvider
	ingress := v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: t.CreateIngressAnnotations(),
		},
		Spec: v1beta1.IngressSpec{
			TLS: tls,
			Rules: []v1beta1.IngressRule{
				v1beta1.IngressRule{
					// Use a dummy host name, otherwise validation fails
					Host:             ManagedCert,
					IngressRuleValue: v1beta1.IngressRuleValue{},
				},
			},
		},
	}
	return &ingress
}

// CleanUpIngressAndTLSSecrets deletes the given Ingress as well as it's configured Secrets.
func CleanUpIngressAndTLSSecrets(client kubernetes.Interface, ingress *v1beta1.Ingress, logger *log.Entry) error {
	logger.Infof("Deleteting Ingress resource %s and related TLS secrets", ingress.Name)
	err := client.Extensions().Ingresses(ingress.Namespace).Delete(ingress.Name, &metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		logger.Errorf("Failed deleteting Ingress resource %s.", ingress.Name)
		return err
	}

	for _, tls := range ingress.Spec.TLS {
		err = client.CoreV1().Secrets(ingress.Namespace).Delete(tls.SecretName, &metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			logger.Errorf("Failed deleteting Secret %s.", ingress.Name)
			return err
		}
	}
	return nil
}

// CreateRecorder creates an EventRecorder for the given client.
func CreateRecorder(kubeClient kubernetes.Interface, logger *log.Entry, componentName string) record.EventRecorder {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(logger.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: typedcorev1.New(kubeClient.CoreV1().RESTClient()).Events("")})
	return eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: componentName})
}

// ControlClusterResourceFilter is a helper object that has filter functionalities on
// K8s resources managed by Cert-Broker in the control cluster.
// The scope of the filter is also defined by the given Namespace.
type ControlClusterResourceFilter struct {
	Namespace string
}

// FilterMethod returns a method that returns whether a K8s resource of an observed
// Namespace is managed by Cert-Broker.
func (f *ControlClusterResourceFilter) FilterMethod() func(object interface{}) bool {
	return func(obj interface{}) bool {
		k8sResource, ok := obj.(metav1.Object)
		if !ok {
			return false
		}
		namespace, name := SplitNamespace(k8sResource.GetName())
		return len(namespace) > 0 && len(name) > 0 && k8sResource.GetNamespace() == f.Namespace
	}
}

func init() {
	source := rand.NewSource(time.Now().UnixNano())
	ingressClass = ManagedCert + "-" + strconv.Itoa(rand.New(source).Int())
}

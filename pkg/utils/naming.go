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
	"strings"

	"k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// GardenPurpose is the key of a label which is used for managed resources.
	GardenPurpose = "garden.sapcloud.io/purpose"
	// GardenCount is the key for count value
	GardenCount = "garden.sapcloud.io/count"
	// ManagedCert is a common identifier for Cert-Broker.
	ManagedCert = "managed-cert"
	// IngressRel is the key of a label which identifies the resource's Ingress relation.
	IngressRel = "garden.sapcloud.io/ingress-rel"
	// CertManagerGroup is the group of the Certificate object
	CertManagerGroup = "certmanager.k8s.io"
	// CertificateKind is the name of Cert-Manager's certificate
	CertificateKind = "Certificate"
	// CertificateResource is the resource name of Cert-Manager's certificate
	CertificateResource = "certificates"
	// CertificateVersion is the version for Cert-Manager's certificate
	CertificateVersion = "v1alpha1"
	// IngressKind is the name of the Ingress kind
	IngressKind = "Ingress"
)

const (
	nameSeparator  = "."
	numOfTokens    = 3
	namespaceIndex = 1
	nameIndex      = 2
)

// CertGvr is the Group, Version, Resource information for Cert-Manager's Certificate
var CertGvr = schema.GroupVersionResource{
	Group:    CertManagerGroup,
	Version:  CertificateVersion,
	Resource: CertificateResource,
}

// CertGvr is the Group, Version, Kind information for Cert-Manager's Certificate
// var CertGvk = schema.GroupVersionKind{
// 	Group:   CertManagerGroup,
// 	Version: CertificateVersion,
// 	Kind:    CertificateKind,
// }

// SplitNamespace cuts out the namespaces and name portion of the given string.
// The string must have a format used by the control cluster,
// i.e. "cert-broker.<target-cluster-namespace>.<name>"
// If the format varies, namespace and name will be empty.
func SplitNamespace(combinedString string) (namespace, name string) {
	parts := strings.SplitN(combinedString, nameSeparator, numOfTokens)
	size := len(parts)
	if size == 1 {
		return "", combinedString
	} else if size == numOfTokens {
		return parts[namespaceIndex], parts[nameIndex]
	}
	return "", ""
}

// CreateNameForControlCluster creates a resource name used by the control cluster.
// This is especially used to avoid name clashes in the control cluster.
func CreateNameForControlCluster(namespace string, name string) string {
	return ManagedCert + nameSeparator + namespace + nameSeparator + name
}

// FilterOnIngressLables takes an Ingress and returns whether it has a label
// dedicated to resources managed by Cert-Broker.
func FilterOnIngressLables(obj interface{}) bool {
	ing, ok := obj.(*v1beta1.Ingress)
	if !ok {
		return false
	}
	return ing.Labels[GardenPurpose] == ManagedCert
}

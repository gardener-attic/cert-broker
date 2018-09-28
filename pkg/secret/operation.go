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
	"github.com/gardener/cert-broker/pkg/utils"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *Controller) createSecret(controlSecret *v1.Secret) error {
	namespace, name := utils.SplitNamespace(controlSecret.Name)
	targetSecret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: controlSecret.Data,
	}
	logger.Infof("Creating secret %s/%s in target cluster", namespace, targetSecret.Name)
	_, err := c.targetCtx.Client.Core().Secrets(namespace).Create(targetSecret)
	return err
}

func (c *Controller) updateSecret(controlSecret, targetSecret *v1.Secret) error {
	targetCopy := targetSecret.DeepCopy()
	targetCopy.Data = controlSecret.Data
	logger.Infof("Updating secret %s/%s in target cluster", targetSecret.Namespace, targetCopy.Name)
	_, err := c.targetCtx.Client.CoreV1().Secrets(targetSecret.Namespace).Update(targetCopy)
	return err
}

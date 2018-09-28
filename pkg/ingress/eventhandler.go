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

	"k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// EventHandler dispatches events to a queue of type RateLimitingInterface.
type EventHandler struct {
	Queue workqueue.RateLimitingInterface
}

// OnAdd adds the object to the queue in case of an add event.
func (e *EventHandler) OnAdd(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		runtime.HandleError(err)
		return
	}
	e.Queue.AddRateLimited(key)
}

// OnUpdate takes two Ingress objects and puts the newObj to the queue
// if the TLS object differs.
func (e *EventHandler) OnUpdate(oldObj, newObj interface{}) {
	oldIngress := oldObj.(*v1beta1.Ingress)
	newIngress := newObj.(*v1beta1.Ingress)
	if reflect.DeepEqual(oldIngress.Spec.TLS, newIngress.Spec.TLS) {
		return
	}
	e.OnAdd(newObj)
}

// OnDelete adds the object to the queue in case of a deletion event.
func (e *EventHandler) OnDelete(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err == nil {
		e.Queue.AddRateLimited(key)
	}
}

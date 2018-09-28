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
	"reflect"

	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// EventHandler dispatches events to a queue of type RateLimitingInterface.
type EventHandler struct {
	Queue workqueue.RateLimitingInterface
}

// OnAdd adds the object of a certain namespace to the queue if the obj's name
// alings to the anticipated control cluster pattern.
func (e *EventHandler) OnAdd(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		runtime.HandleError(err)
		return
	}
	e.Queue.AddRateLimited(key)
}

// OnUpdate adds the object of a certain namespace to the queue if the obj's name
// alings to the anticipated control cluster pattern and oldObj differs from newObj.
func (e *EventHandler) OnUpdate(oldObj, newObj interface{}) {
	if reflect.DeepEqual(oldObj, newObj) {
		return
	}
	e.OnAdd(newObj)
}

// OnDelete events are ignores
func (e *EventHandler) OnDelete(obj interface{}) {}

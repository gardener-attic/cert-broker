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

package events_test

import (
	"time"

	"github.com/gardener/cert-broker/pkg/events"
	"github.com/gardener/cert-broker/pkg/utils"
	. "github.com/onsi/ginkgo" //. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
)

var _ = Describe("events", func() {
	var (
		eventhandler *events.EventHandler
		queue        workqueue.RateLimitingInterface
	)

	BeforeEach(func() {
		queue = workqueue.NewRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(0*time.Second, 0*time.Second))
		eventhandler = &events.EventHandler{
			Queue: queue,
		}
		Expect(queue.Len()).To(BeZero())
	})

	Describe("eventhandler", func() {
		Describe("#OnAdd", func() {
			It("should not be added", func() {
				event := &v1.Event{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{utils.GardenCount: "1"},
					},
					Count: 1,
				}
				eventhandler.OnAdd(event)
				Expect(queue.Len()).To(BeZero())
			})

			It("should be added", func() {
				event := &v1.Event{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{utils.GardenCount: "1"},
					},
					Count: 2,
				}
				eventhandler.OnAdd(event)
				Expect(queue.Len()).Should(Equal(1))
			})

			It("should be added", func() {
				event := &v1.Event{
					Count: 2,
				}
				eventhandler.OnAdd(event)
				Expect(queue.Len()).Should(Equal(1))
			})
		})
	})
})

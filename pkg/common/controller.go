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

package common

import (
	"fmt"
	"sync"
	"time"

	"github.com/gardener/cert-broker/pkg/utils"
	log "github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// Handler is an interface which this generic controller uses for
type Handler interface {
	Sync(key string) error
	GetInformerSyncs() []cache.InformerSynced
	GetWorkQueue() workqueue.RateLimitingInterface
}

// Controller holds information about the traget and control cluster.
type Controller struct {
	Handler
	workerwg sync.WaitGroup
	logger   *log.Entry
}

// NewController creates a new instance of Controller which in turn
// is capable of replicating Ingress resources.
func NewController(handler Handler, logger *log.Entry) *Controller {
	controller := &Controller{
		Handler: handler,
		logger:  logger,
	}
	return controller
}

func certificateIsInvolved(obj interface{}) bool {
	event, ok := obj.(*v1.Event)
	if !ok {
		return false
	}
	return event.InvolvedObject.APIVersion == utils.CertManagerGroup && event.InvolvedObject.Kind == utils.CertificateKind
}

// Start starts the control loop.
func (c *Controller) Start(worker int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	c.logger.Debug("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(
		stopCh,
		c.GetInformerSyncs()...,
	); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	c.logger.Info("Starting workers")
	for i := 0; i < worker; i++ {
		c.workerwg.Add(1)
		go wait.Until(c.runWorker, time.Second, stopCh)
		c.logger.Debugf("Started worker %d", i)
	}
	c.logger.Debug("Started all workers")

	<-stopCh
	c.logger.Info("Shutting down workers")
	c.GetWorkQueue().ShutDown()
	c.workerwg.Wait()
	c.logger.Info("All workers have stopped processing")
	return nil
}

func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
	// Gracefully stopped worker.
	c.workerwg.Done()
}

func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.GetWorkQueue().Get()

	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.GetWorkQueue().Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			c.GetWorkQueue().Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := c.Sync(key); err != nil {
			return fmt.Errorf("error syncing '%s': %s", key, err.Error())
		}
		c.GetWorkQueue().Forget(obj)
		c.logger.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		c.logger.Infof("Re-queuing item '%v'", obj)
		c.GetWorkQueue().AddRateLimited(obj)
		return true
	}
	return true
}

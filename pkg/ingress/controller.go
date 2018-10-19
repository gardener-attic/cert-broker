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
	"sync"
	"time"

	"github.com/gardener/cert-broker/pkg/utils"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/listers/core/v1"
	lv1beta1 "k8s.io/client-go/listers/extensions/v1beta1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
)

const controllerAgentName = "ingress-controller"

var logger *log.Entry

// Controller holds information about the traget and control cluster.
type Controller struct {
	controlCtx      *ControlClusterContext
	targetCtx       *TargetClusterContext
	ingressTemplate *utils.IngressTemplate
	managedDomains  []string
	workqueue       workqueue.RateLimitingInterface
	recorder        record.EventRecorder
	workerwg        sync.WaitGroup
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
func NewController(controlCtx *ControlClusterContext, targetCtx *TargetClusterContext, ingressTemplate *utils.IngressTemplate, managedDomains []string) *Controller {
	controller := &Controller{
		controlCtx:      controlCtx,
		targetCtx:       targetCtx,
		ingressTemplate: ingressTemplate,
		managedDomains:  managedDomains,
		workqueue:       workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Ingresses"),
		recorder:        utils.CreateRecorder(targetCtx.Client, logger, "Cert-Broker-Ingress-Control"),
	}
	controller.targetCtx.IngressInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: utils.FilterOnIngressLables,
		Handler:    &EventHandler{Queue: controller.workqueue},
	})
	return controller
}

// Start starts the control loop.
func (c *Controller) Start(worker int, stopCh <-chan struct{}) error {
	defer runtime.HandleCrash()
	logger.Debug("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(
		stopCh,
		c.targetCtx.IngressSync,
		c.controlCtx.IngressSync,
		c.controlCtx.SecretSync); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	logger.Info("Starting workers")
	for i := 0; i < worker; i++ {
		c.workerwg.Add(1)
		go wait.Until(c.runWorker, time.Second, stopCh)
		logger.Debugf("Started worker %d", i)
	}
	logger.Debug("Started all workers")

	<-stopCh
	logger.Info("Shutting down workers")
	c.workqueue.ShutDown()
	c.workerwg.Wait()
	logger.Info("All workers have stopped processing")
	return nil
}

func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
	// Gracefully stopped worker.
	c.workerwg.Done()
}

func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			c.workqueue.Forget(obj)
			runtime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := c.syncHandler(key); err != nil {
			return fmt.Errorf("error syncing '%s': %s", key, err.Error())
		}
		c.workqueue.Forget(obj)
		logger.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		runtime.HandleError(err)
		logger.Infof("Re-queuing item '%v'", obj)
		c.workqueue.AddRateLimited(obj)
		return true
	}
	return true
}

func (c *Controller) syncHandler(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	controlIngressName := utils.CreateNameForControlCluster(namespace, name)

	// Delete Ingress in control cluster.
	targetIngress, err := c.targetCtx.IngressLister.Ingresses(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			return c.cleanUpIngressAndSecrets(controlIngressName)
		}
		return err
	}

	// Create new Ingress.
	controlIngress, err := c.controlCtx.IngressLister.Ingresses(c.controlCtx.ResourceNamespace).Get(controlIngressName)
	if err != nil {
		if errors.IsNotFound(err) {
			err := c.createIngress(controlIngressName, targetIngress)
			return err
		}
		return err
	}

	// Update Ingress if TLS information has changed.
	return c.updateIngress(targetIngress, controlIngress)
}

func init() {
	logger = log.WithFields(log.Fields{"APP": controllerAgentName})
}

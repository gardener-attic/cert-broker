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

package cleaner

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
	lv1beta1 "k8s.io/client-go/listers/extensions/v1beta1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

const (
	controllerAgentName = "ingress-cleaner"
	requeuePeriod       = time.Hour
)

var (
	logger      *log.Entry
	rateLimiter = workqueue.DefaultControllerRateLimiter()
)

// Controller holds information about the traget and control cluster.
type Controller struct {
	controlCtx         *ControlClusterContext
	targetCtx          *TargetClusterContext
	updateIngresses    bool
	annotationTemplate utils.CreateIngressAnnotation
	workqueue          workqueue.DelayingInterface
	workerwg           sync.WaitGroup
}

// ControlClusterContext holds information about the control cluster.
type ControlClusterContext struct {
	// ResourceNamespace determines the location of replicated Ingress resources.
	ResourceNamespace string
	Client            kubernetes.Interface
	IngressInformer   cache.SharedIndexInformer
	IngressLister     lv1beta1.IngressLister
	IngressSync       cache.InformerSynced
}

// TargetClusterContext holds information about the target cluster.
type TargetClusterContext struct {
	IngressLister lv1beta1.IngressLister
	IngressSync   cache.InformerSynced
}

// NewController creates a new instance of Controller which in turn
// is capable of deleting Ingress resources in the control clusters
// which don't have a counterpart in the target cluster.
func NewController(
	controlCtx *ControlClusterContext,
	targetCtx *TargetClusterContext,
	updateIngresses bool,
	annotationTemplate utils.CreateIngressAnnotation,
) *Controller {
	controller := &Controller{
		controlCtx:         controlCtx,
		targetCtx:          targetCtx,
		updateIngresses:    updateIngresses,
		annotationTemplate: annotationTemplate,
		workqueue:          workqueue.NewNamedDelayingQueue("Ingress"),
	}
	controlClusterFilter := utils.ControlClusterResourceFilter{Namespace: controlCtx.ResourceNamespace}
	controller.controlCtx.IngressInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controlClusterFilter.FilterMethod(),
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
		c.controlCtx.IngressSync); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	logger.Info("Starting workers")
	for i := 0; i < worker; i++ {
		c.workerwg.Add(1)
		go wait.Until(c.runWorker, time.Second, stopCh)
		logger.Debugf("Started worker %d", i)
	}
	logger.Debugf("Started all workers")

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
			runtime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		requing, err := c.syncHandler(key)
		if err != nil {
			return fmt.Errorf("error syncing '%s': %s", key, err.Error())
		}
		if requing {
			logger.Infof("Item '%v' is rechecked for cleanup in %v", obj, requeuePeriod)
			c.workqueue.AddAfter(obj, requeuePeriod)
		}
		rateLimiter.Forget(obj)
		logger.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		runtime.HandleError(err)
		time := rateLimiter.When(obj)
		logger.Infof("Re-queuing item '%v' in %v", obj, time)
		c.workqueue.AddAfter(obj, time)
		return true
	}
	return true
}

func (c *Controller) syncHandler(key string) (bool, error) {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return true, nil
	}

	targetNamespace, targetIngressName := utils.SplitNamespace(name)

	controlIngress, err := c.controlCtx.IngressLister.Ingresses(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return true, err
	}

	targetIngress, err := c.targetCtx.IngressLister.Ingresses(targetNamespace).Get(targetIngressName)
	if err != nil {
		// Ingress on target cluster was deleted.
		if errors.IsNotFound(err) {
			logger.Infof("Ingress %s has no counterpart in target cluster", controlIngress.Name)
			return false, utils.CleanUpIngressAndTLSSecrets(c.controlCtx.Client, controlIngress, logger)
		}
		return true, err
	}

	// Managed label was removed from target ingress
	if !utils.FilterOnIngressLables(targetIngress) {
		logger.Infof("Managed label was removed from target ingress %s/%s", targetIngress.Namespace, targetIngress.Name)
		return false, utils.CleanUpIngressAndTLSSecrets(c.controlCtx.Client, controlIngress, logger)
	}

	// Ingress in control cluster still has a counterpart in the target cluster.
	if c.updateIngresses {
		controlIngressCopy := controlIngress.DeepCopy()
		annotations := c.annotationTemplate()
		for annotation, value := range annotations {
			// Override annotation values given by template function.
			controlIngressCopy.Annotations[annotation] = value
		}
		if _, err := c.controlCtx.Client.Extensions().Ingresses(namespace).Update(controlIngressCopy); err != nil {
			return true, err
		}
		logger.Infof("Updated Ingress annotations of %s/%s", controlIngress.Namespace, controlIngress.Name)
	}
	return true, nil
}

func init() {
	logger = log.WithFields(log.Fields{"APP": controllerAgentName})
}

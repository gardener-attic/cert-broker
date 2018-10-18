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

package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gardener/cert-broker/pkg/cleaner"
	"github.com/gardener/cert-broker/pkg/events"
	"github.com/gardener/cert-broker/pkg/ingress"
	"github.com/gardener/cert-broker/pkg/secret"
	"github.com/gardener/cert-broker/pkg/utils"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

// AppName holds the name of the application.
const AppName = "cert-borker"

var logger *log.Entry

// CertBroker is used to configure and start the controller.
type CertBroker struct {
	ControllerOptions *Options
}

func (cb *CertBroker) startCertBorker(out, errOut io.Writer, stopCh <-chan struct{}) error {
	targetClusterConfig, err := clientcmd.BuildConfigFromFlags("", cb.ControllerOptions.TargetClusterKubeconf)
	if err != nil {
		return fmt.Errorf("error getting config instance for target cluster: %v", err)
	}

	controlClusterConfig, err := clientcmd.BuildConfigFromFlags("", cb.ControllerOptions.ControlClusterKubeconf)
	if err != nil {
		return fmt.Errorf("error getting config instance for control cluster: %v", err)
	}

	targetClusterClient, err := kubernetes.NewForConfig(targetClusterConfig)
	if err != nil {
		return fmt.Errorf("error getting clientset instance for target cluster: %v", err)
	}

	controlClusterClient, err := kubernetes.NewForConfig(controlClusterConfig)
	if err != nil {
		return fmt.Errorf("error getting clientset instance for control cluster: %v", err)
	}

	targetClientInformerFactory := informers.NewSharedInformerFactory(targetClusterClient, time.Second*30)
	controlClientInformerFactory := informers.NewSharedInformerFactory(controlClusterClient, time.Second*30)

	dynControlClient, err := dynamic.NewForConfig(controlClusterConfig)
	if err != nil {
		return fmt.Errorf("error getting clientset instance for control cluster: %v", err)
	}

	certificatesInformer := cache.NewSharedIndexInformer(&cache.ListWatch{
		ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
			return dynControlClient.Resource(utils.CertGvr).List(options)
		},
		WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
			return dynControlClient.Resource(utils.CertGvr).Watch(options)
		},
	}, nil, time.Second*30, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})

	certificatesLister := cache.NewGenericLister(certificatesInformer.GetIndexer(), utils.CertGvr.GroupResource())

	ingressTemplate := utils.CreateIngressTemplate(
		cb.ControllerOptions.ClusterIssuer,
		cb.ControllerOptions.AcmeChallengeType,
		cb.ControllerOptions.AcmeDNS01Provider,
	)

	ingressController := ingress.NewController(
		&ingress.ControlClusterContext{
			ResourceNamespace: cb.ControllerOptions.ResourceNamespace,
			Client:            controlClusterClient,
			IngressLister:     controlClientInformerFactory.Extensions().V1beta1().Ingresses().Lister(),
			IngressSync:       controlClientInformerFactory.Extensions().V1beta1().Ingresses().Informer().HasSynced,
			SecretLister:      controlClientInformerFactory.Core().V1().Secrets().Lister(),
			SecretSync:        controlClientInformerFactory.Core().V1().Secrets().Informer().HasSynced,
		},
		&ingress.TargetClusterContext{
			Client:          targetClusterClient,
			IngressInformer: targetClientInformerFactory.Extensions().V1beta1().Ingresses().Informer(),
			IngressLister:   targetClientInformerFactory.Extensions().V1beta1().Ingresses().Lister(),
			IngressSync:     targetClientInformerFactory.Extensions().V1beta1().Ingresses().Informer().HasSynced,
		},
		ingressTemplate,
	)

	ingressCleaner := cleaner.NewController(
		&cleaner.ControlClusterContext{
			ResourceNamespace: cb.ControllerOptions.ResourceNamespace,
			Client:            controlClusterClient,
			IngressInformer:   controlClientInformerFactory.Extensions().V1beta1().Ingresses().Informer(),
			IngressLister:     controlClientInformerFactory.Extensions().V1beta1().Ingresses().Lister(),
			IngressSync:       controlClientInformerFactory.Extensions().V1beta1().Ingresses().Informer().HasSynced,
		},
		&cleaner.TargetClusterContext{
			IngressLister: targetClientInformerFactory.Extensions().V1beta1().Ingresses().Lister(),
			IngressSync:   targetClientInformerFactory.Extensions().V1beta1().Ingresses().Informer().HasSynced,
		},
		cb.ControllerOptions.UpdateControlIngress,
		ingressTemplate.CreateIngressAnnotations,
	)

	secretController := secret.NewController(
		&secret.ControlClusterContext{
			ResourceNamespace: cb.ControllerOptions.ResourceNamespace,
			Client:            controlClusterClient,
			SecretLister:      controlClientInformerFactory.Core().V1().Secrets().Lister(),
			SecretInformer:    controlClientInformerFactory.Core().V1().Secrets().Informer(),
			SecretSync:        controlClientInformerFactory.Core().V1().Secrets().Informer().HasSynced,
		},
		&secret.TargetClusterContext{
			Client:        targetClusterClient,
			SecretLister:  targetClientInformerFactory.Core().V1().Secrets().Lister(),
			SecretSync:    targetClientInformerFactory.Core().V1().Secrets().Informer().HasSynced,
			IngressLister: targetClientInformerFactory.Extensions().V1beta1().Ingresses().Lister(),
			IngressSync:   targetClientInformerFactory.Extensions().V1beta1().Ingresses().Informer().HasSynced,
		},
	)

	eventController := events.NewController(
		&events.ControlClusterContext{
			EventInformer:      controlClientInformerFactory.Core().V1().Events().Informer(),
			EventLister:        controlClientInformerFactory.Core().V1().Events().Lister(),
			EventSync:          controlClientInformerFactory.Core().V1().Events().Informer().HasSynced,
			CertificatesLister: certificatesLister,
			CertificatesSync:   certificatesInformer.HasSynced,
		},
		&events.TargetClusterContext{
			Client:        targetClusterClient,
			IngressLister: targetClientInformerFactory.Extensions().V1beta1().Ingresses().Lister(),
			IngressSync:   targetClientInformerFactory.Extensions().V1beta1().Ingresses().Informer().HasSynced,
		},
	)

	run := func(_ <-chan struct{}) {
		targetClientInformerFactory.Start(stopCh)
		controlClientInformerFactory.Start(stopCh)
		go certificatesInformer.Run(stopCh)

		var controllerWg sync.WaitGroup

		go func() {
			defer controllerWg.Done()
			controllerWg.Add(1)
			if err := ingressController.Start(int(cb.ControllerOptions.IngressWorkerCount), stopCh); err != nil {
				logger.Errorf("error starting the ingress controller: %v", err)
			}
		}()

		go func() {
			defer controllerWg.Done()
			controllerWg.Add(1)
			if err := secretController.Start(int(cb.ControllerOptions.SecretWorkerCount), stopCh); err != nil {
				logger.Errorf("error starting the secret controller: %v", err)
			}
		}()

		go func() {
			defer controllerWg.Done()
			controllerWg.Add(1)
			if err := ingressCleaner.Start(int(cb.ControllerOptions.CleanupWorkerCount), stopCh); err != nil {
				logger.Errorf("error starting the ingress cleaner: %v", err)
			}
		}()

		go func() {
			defer controllerWg.Done()
			controllerWg.Add(1)
			if err := eventController.Start(int(cb.ControllerOptions.CleanupWorkerCount), stopCh); err != nil {
				logger.Errorf("error starting the event controlle: %v", err)
			}
		}()

		<-stopCh
		controllerWg.Wait()

		// Gracefully exit application if K8s leader election is used.
		if cb.ControllerOptions.LeaderElection {
			os.Exit(0)
		}
	}

	if cb.ControllerOptions.LeaderElection {
		return cb.runWithLeaderElection(run, controlClusterConfig)
	}

	// Start controllers if K8s leader election is not used.
	run(stopCh)

	return nil
}

func (cb *CertBroker) validateControllerOptions() error {
	if _, err := os.Stat(cb.ControllerOptions.TargetClusterKubeconf); os.IsNotExist(err) {
		return fmt.Errorf("File %s does not exist", cb.ControllerOptions.TargetClusterKubeconf)
	}
	if len(cb.ControllerOptions.ControlClusterKubeconf) > 0 {
		if _, err := os.Stat(cb.ControllerOptions.ControlClusterKubeconf); os.IsNotExist(err) {
			return fmt.Errorf("File %s does not exist", cb.ControllerOptions.ControlClusterKubeconf)
		}
	}
	if len(cb.ControllerOptions.AcmeChallengeType) < 1 {
		return fmt.Errorf("ACME challenge type must be provided")
	} else if strings.EqualFold(cb.ControllerOptions.AcmeChallengeType, defaultAcmeChallenge) {
		if len(cb.ControllerOptions.AcmeDNS01Provider) < 1 {
			return fmt.Errorf("DNS01 provider must be provided")
		}
	}
	return nil
}

// NewCertBroker returns a new instance to run the Cert-Broker.
func NewCertBroker(out, errOut io.Writer, stopCh <-chan struct{}) *cobra.Command {

	var cb CertBroker

	cmd := &cobra.Command{
		Use:   AppName,
		Short: "Cert-Broker triggers certificate management for Ingress resources across clusters",
		Long: `Cert-Broker triggers certificate management for Ingress resouces by copying them from a plain cluster (target cluster) to another cluster which runs an instance of Cert-Manager with Ingrss-Shim (control cluster).
Subsequently the generated certificate, i.e. Kubernetes secret, is copied back to the target cluster.`,
		RunE: func(c *cobra.Command, args []string) error {
			if err := cb.validateControllerOptions(); err != nil {
				return err
			}
			return cb.startCertBorker(out, errOut, stopCh)
		},
	}

	cb = CertBroker{
		ControllerOptions: NewControllerOptions(cmd.Flags()),
	}

	return cmd
}

func init() {
	logger = log.WithFields(log.Fields{"APP": AppName})
}

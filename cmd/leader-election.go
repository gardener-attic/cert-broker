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
	"context"
	"fmt"
	"os"
	"time"

	"github.com/gardener/cert-broker/pkg/utils"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

const (
	stdLeaseDuration = 60 * time.Second
	stdRenewDeadline = 30 * time.Second
	stdRetryPeiod    = 15 * time.Second
)

func (cb *CertBroker) runWithLeaderElection(ctx context.Context, run func(context.Context), controlClusterConfig *restclient.Config) error {
	leaderElectionClient, err := kubernetes.NewForConfig(controlClusterConfig)
	if err != nil {
		return fmt.Errorf("error getting clientset for leader election: %v", err)
	}
	leaderElectionConfig, err := cb.makeLeaderElectionConfig(leaderElectionClient)
	if err != nil {
		return err
	}

	leaderElectionConfig.Callbacks = leaderelection.LeaderCallbacks{
		OnStartedLeading: run,
		OnStoppedLeading: func() {
			logger.Info("Lost leadership, cleaning up.")
		},
	}
	leaderElector, err := leaderelection.NewLeaderElector(*leaderElectionConfig)
	if err != nil {
		return fmt.Errorf("couldn't create leader elector: %v", err)
	}
	leaderElector.Run(ctx)
	return nil
}

func (cb *CertBroker) makeLeaderElectionConfig(client *kubernetes.Clientset) (*leaderelection.LeaderElectionConfig, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("unable to get hostname: %v for leader election", err)
	}

	lock, err := resourcelock.New(resourcelock.ConfigMapsResourceLock,
		cb.ControllerOptions.ResourceNamespace,
		AppName,
		client.CoreV1(),
		resourcelock.ResourceLockConfig{
			Identity:      hostname,
			EventRecorder: utils.CreateRecorder(client, logger, "Cert-Broker-Leader-Election"),
		},
	)

	if err != nil {
		return nil, fmt.Errorf("couldn't create resource lock: %v for leader election", err)
	}

	return &leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: stdLeaseDuration,
		RenewDeadline: stdRenewDeadline,
		RetryPeriod:   stdRetryPeiod,
	}, nil
}

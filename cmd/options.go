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
	"time"

	"github.com/spf13/pflag"
)

const (
	defaultSyncInterval         = 30 * time.Second
	defaultLeaderElection       = false
	defaultUpdateContronIngress = true
	defaultWorkerCount          = 4
	defaultCleanupCount         = 1
	defaultResoureceNamespace   = "default"
	defaultAcmeChallenge        = "dns01"
)

var defaultManagedDomains = []string{}

// Options represent configuration settings for a running instance of Cert-Broker.
type Options struct {
	TargetClusterKubeconf  string
	ControlClusterKubeconf string
	ResourceNamespace      string
	ClusterIssuer          string
	AcmeChallengeType      string
	AcmeDNS01Provider      string
	ManagedDomains         []string
	UpdateControlIngress   bool
	LeaderElection         bool
	IngressWorkerCount     uint
	SecretWorkerCount      uint
	CleanupWorkerCount     uint
	EventWorkerCount       uint
	SyncInterval           time.Duration
}

func (options *Options) addFlags(fs *pflag.FlagSet) {
	fs.StringVarP(&options.TargetClusterKubeconf, "target-cluster-kube-config", "s", "", ""+
		"Path to the target cluster's Kubeconfig")
	fs.StringVarP(&options.ControlClusterKubeconf, "control-cluster-kube-config", "c", "", ""+
		"Path to the control cluster's Kubeconfig where an instance of Cert-Manager is running")
	fs.StringVarP(&options.ResourceNamespace, "resource-namespace", "n", defaultResoureceNamespace, ""+
		"Namespace to be used in control cluster")
	fs.StringVar(&options.ClusterIssuer, "cluster-issuer", "", ""+
		"Name of cluster-issuer in control namespace")
	fs.StringVar(&options.AcmeChallengeType, "acme-challenge-type", defaultAcmeChallenge, ""+
		"ACME challenge type to be set in replicated Ingress")
	fs.StringVar(&options.AcmeDNS01Provider, "acme-dns01-provider", "", ""+
		"Name of the DNS01 provider if ACME challenge type is DNS01")
	fs.StringArrayVar(&options.ManagedDomains, "managed-domain", defaultManagedDomains, ""+
		"A list of domains being considerd by Cert-Broker. Domains apart from that list will be ignored.")
	fs.BoolVar(&options.UpdateControlIngress, "update-ingress", defaultUpdateContronIngress, ""+
		"Determins whether existing Ingress resources in the control cluster should be updated."+
		"Needed if issuer information has changed.")
	fs.BoolVar(&options.LeaderElection, "leader-election", defaultLeaderElection, ""+
		"Use leader election")
	fs.DurationVarP(&options.SyncInterval, "sync-intervall", "i", defaultSyncInterval, ""+
		"Interval to be used to copy certificates from control to target cluster")
	fs.UintVar(&options.IngressWorkerCount, "ingress-workers", defaultWorkerCount, ""+
		"Worker count for the Ingress replication")
	fs.UintVar(&options.SecretWorkerCount, "secret-workers", defaultWorkerCount, ""+
		"Worker count for the Secret replication")
	fs.UintVar(&options.CleanupWorkerCount, "cleanup-workers", defaultCleanupCount, ""+
		"Worker count for the Ingress clean-up")
	fs.UintVar(&options.EventWorkerCount, "event-workers", defaultWorkerCount, ""+
		"Worker count for the Event propagation")
}

// NewControllerOptions adds configured flags and returns a pointer to a newly created Options struct.
func NewControllerOptions(fs *pflag.FlagSet) *Options {
	options := &Options{}
	options.addFlags(fs)
	return options
}

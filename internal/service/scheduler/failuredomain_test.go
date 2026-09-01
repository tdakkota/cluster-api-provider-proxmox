/*
Copyright 2023-2026 IONOS Cloud.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package scheduler

import (
	"testing"

	"github.com/stretchr/testify/require"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	infrav1 "github.com/ionos-cloud/cluster-api-provider-proxmox/api/v1alpha2"
	"github.com/ionos-cloud/cluster-api-provider-proxmox/pkg/scope"
)

func TestAllowedNodes(t *testing.T) {
	domains := []infrav1.ProxmoxFailureDomain{
		{Name: "rack-1", Nodes: []string{"pve1", "pve2"}},
		{Name: "rack-2", Nodes: []string{"pve3"}},
	}

	tests := []struct {
		name          string
		domains       []infrav1.ProxmoxFailureDomain
		clusterNodes  []string
		machineNodes  []string
		capiDomain    string
		machineDomain string
		want          []string
	}{
		{
			name:         "falls back to the cluster",
			clusterNodes: []string{"pve1", "pve2", "pve3"},
			want:         []string{"pve1", "pve2", "pve3"},
		},
		{
			name:         "machine replaces the cluster",
			clusterNodes: []string{"pve1", "pve2", "pve3"},
			machineNodes: []string{"pve2"},
			want:         []string{"pve2"},
		},
		{
			name:         "domain replaces both",
			domains:      domains,
			clusterNodes: []string{"pve1", "pve2", "pve3"},
			machineNodes: []string{"pve1"},
			capiDomain:   "rack-2",
			want:         []string{"pve3"},
		},
		{
			name:          "domain on the proxmox machine applies",
			domains:       domains,
			clusterNodes:  []string{"pve1", "pve2", "pve3"},
			machineDomain: "rack-1",
			want:          []string{"pve1", "pve2"},
		},
		{
			name:          "cluster api assignment wins over the proxmox machine",
			domains:       domains,
			clusterNodes:  []string{"pve1", "pve2", "pve3"},
			capiDomain:    "rack-2",
			machineDomain: "rack-1",
			want:          []string{"pve3"},
		},
		{
			name:         "undeclared domain falls through",
			domains:      domains,
			clusterNodes: []string{"pve1", "pve2", "pve3"},
			machineNodes: []string{"pve2"},
			capiDomain:   "rack-9",
			want:         []string{"pve2"},
		},
		{
			name: "nothing declared anywhere",
			want: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			machineScope := &scope.MachineScope{
				Machine: &clusterv1.Machine{
					Spec: clusterv1.MachineSpec{FailureDomain: test.capiDomain},
				},
				ProxmoxMachine: &infrav1.ProxmoxMachine{
					Spec: infrav1.ProxmoxMachineSpec{
						AllowedNodes:  test.machineNodes,
						FailureDomain: test.machineDomain,
					},
				},
				InfraCluster: &scope.ClusterScope{
					ProxmoxCluster: &infrav1.ProxmoxCluster{
						Spec: infrav1.ProxmoxClusterSpec{
							AllowedNodes:   test.clusterNodes,
							FailureDomains: test.domains,
						},
					},
				},
			}

			require.Equal(t, test.want, AllowedNodes(machineScope))
		})
	}
}

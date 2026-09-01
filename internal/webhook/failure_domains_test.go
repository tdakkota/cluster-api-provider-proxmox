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

package webhook

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	infrav1 "github.com/ionos-cloud/cluster-api-provider-proxmox/api/v1alpha2"
)

func zoneConfig(names ...string) []infrav1.ZoneConfigSpec {
	configs := make([]infrav1.ZoneConfigSpec, 0, len(names))
	for _, name := range names {
		configs = append(configs, infrav1.ZoneConfigSpec{
			Zone:       ptr.To(name),
			DNSServers: []string{"1.1.1.1"},
			IPv4Config: &infrav1.IPConfigSpec{
				Addresses: []string{"10.0.0.10-10.0.0.20"},
				Prefix:    24,
				Gateway:   "10.0.0.1",
			},
		})
	}

	return configs
}

func TestValidateFailureDomains(t *testing.T) {
	gk := schema.GroupKind{Group: "infrastructure.cluster.x-k8s.io", Kind: "ProxmoxCluster"}

	tests := []struct {
		name    string
		spec    infrav1.ProxmoxClusterSpec
		wantErr string
	}{
		{
			name: "no failure domains",
			spec: infrav1.ProxmoxClusterSpec{ZoneConfigs: zoneConfig("a")},
		},
		{
			name: "zone defaults to the domain name",
			spec: infrav1.ProxmoxClusterSpec{
				ZoneConfigs:    zoneConfig("rack-1"),
				FailureDomains: []infrav1.ProxmoxFailureDomain{{Name: "rack-1", Nodes: []string{"pve1"}}},
			},
		},
		{
			name: "explicit zone resolves",
			spec: infrav1.ProxmoxClusterSpec{
				ZoneConfigs:    zoneConfig("net-a"),
				FailureDomains: []infrav1.ProxmoxFailureDomain{{Name: "rack-1", Nodes: []string{"pve1"}, Zone: "net-a"}},
			},
		},
		{
			name: "two domains may share one zone",
			spec: infrav1.ProxmoxClusterSpec{
				ZoneConfigs: zoneConfig("net-a"),
				FailureDomains: []infrav1.ProxmoxFailureDomain{
					{Name: "rack-1", Nodes: []string{"pve1"}, Zone: "net-a"},
					{Name: "rack-2", Nodes: []string{"pve2"}, Zone: "net-a"},
				},
			},
		},
		{
			name: "zone names no zoneConfig entry",
			spec: infrav1.ProxmoxClusterSpec{
				ZoneConfigs:    zoneConfig("net-a"),
				FailureDomains: []infrav1.ProxmoxFailureDomain{{Name: "rack-1", Nodes: []string{"pve1"}, Zone: "missing"}},
			},
			wantErr: "must name a zoneConfig entry",
		},
		{
			name: "a node claimed by two domains",
			spec: infrav1.ProxmoxClusterSpec{
				ZoneConfigs: zoneConfig("rack-1", "rack-2"),
				FailureDomains: []infrav1.ProxmoxFailureDomain{
					{Name: "rack-1", Nodes: []string{"pve1", "pve2"}},
					{Name: "rack-2", Nodes: []string{"pve2", "pve3"}},
				},
			},
			wantErr: `already belongs to failure domain "rack-1"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFailureDomains(&tt.spec, gk, "test")
			if tt.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestProxmoxClusterTemplateValidatesFailureDomains(t *testing.T) {
	template := &infrav1.ProxmoxClusterTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "test-template"},
		Spec: infrav1.ProxmoxClusterTemplateSpec{
			Template: infrav1.ProxmoxClusterTemplateResource{
				Spec: infrav1.ProxmoxClusterSpec{
					ControlPlaneEndpoint: infrav1.APIEndpoint{Host: "10.0.0.100", Port: 6443},
					IPv4Config: &infrav1.IPConfigSpec{
						Addresses: []string{"10.0.0.10-10.0.0.20"},
						Prefix:    24,
						Gateway:   "10.0.0.1",
					},
					ZoneConfigs: zoneConfig("net-a"),
					FailureDomains: []infrav1.ProxmoxFailureDomain{
						{Name: "rack-1", Nodes: []string{"pve1"}, Zone: "missing"},
					},
				},
			},
		},
	}

	v := &ProxmoxClusterTemplate{}

	_, err := v.ValidateCreate(context.Background(), template)
	require.ErrorContains(t, err, "must name a zoneConfig entry")

	_, err = v.ValidateUpdate(context.Background(), template, template)
	require.ErrorContains(t, err, "must name a zoneConfig entry")
}

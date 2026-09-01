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

package v1alpha2

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
)

func TestFailureDomainZoneName(t *testing.T) {
	tests := []struct {
		name   string
		domain ProxmoxFailureDomain
		want   string
	}{
		{"explicit zone", ProxmoxFailureDomain{Name: "rack-1", Zone: "dmz"}, "dmz"},
		{"defaults to name", ProxmoxFailureDomain{Name: "rack-1"}, "rack-1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, test.domain.ZoneName())
		})
	}
}

func TestFailureDomainIsControlPlaneAllowed(t *testing.T) {
	tests := []struct {
		name   string
		domain ProxmoxFailureDomain
		want   bool
	}{
		{"unset defaults to true", ProxmoxFailureDomain{Name: "rack-1"}, true},
		{"explicitly true", ProxmoxFailureDomain{Name: "rack-1", ControlPlane: ptr.To(true)}, true},
		{"explicitly false", ProxmoxFailureDomain{Name: "rack-1", ControlPlane: ptr.To(false)}, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, test.domain.IsControlPlaneAllowed())
		})
	}
}

func TestProxmoxClusterSpecHasZone(t *testing.T) {
	spec := ProxmoxClusterSpec{
		IPv4Config: &IPConfigSpec{Addresses: []string{"10.0.0.2-10.0.0.10"}},
		ZoneConfigs: []ZoneConfigSpec{
			{Zone: ptr.To("dmz")},
		},
	}

	tests := []struct {
		name string
		spec ProxmoxClusterSpec
		zone string
		want bool
	}{
		{"configured zone", spec, "dmz", true},
		{"unknown zone", spec, "nowhere", false},
		{"implicit default zone", spec, "default", true},
		{"default zone without ip config", ProxmoxClusterSpec{}, "default", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, test.spec.HasZone(test.zone))
		})
	}
}

func TestProxmoxClusterGetFailureDomain(t *testing.T) {
	cluster := ProxmoxCluster{
		Spec: ProxmoxClusterSpec{
			FailureDomains: []ProxmoxFailureDomain{
				{Name: "rack-1", Nodes: []string{"pve1", "pve2"}},
				{Name: "rack-2", Nodes: []string{"pve3"}},
			},
		},
	}

	require.Equal(t, []string{"pve3"}, cluster.GetFailureDomain("rack-2").Nodes)
	require.Nil(t, cluster.GetFailureDomain("rack-3"))
	require.Nil(t, cluster.GetFailureDomain(""))
}

func TestProxmoxClusterFailureDomainForNode(t *testing.T) {
	cluster := ProxmoxCluster{
		Spec: ProxmoxClusterSpec{
			FailureDomains: []ProxmoxFailureDomain{
				{Name: "rack-1", Nodes: []string{"pve1", "pve2"}},
				{Name: "rack-2", Nodes: []string{"pve3"}},
			},
		},
	}

	tests := []struct {
		name string
		node string
		want string
	}{
		{"first domain", "pve1", "rack-1"},
		{"second domain", "pve3", "rack-2"},
		{"node in no domain", "pve9", ""},
		{"no node", "", ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, cluster.FailureDomainForNode(test.node))
		})
	}
}

func TestProxmoxClusterZoneForMachine(t *testing.T) {
	cluster := ProxmoxCluster{
		Spec: ProxmoxClusterSpec{
			FailureDomains: []ProxmoxFailureDomain{
				{Name: "rack-1", Nodes: []string{"pve1"}, Zone: "dmz"},
				{Name: "rack-2", Nodes: []string{"pve2"}},
			},
		},
	}

	tests := []struct {
		name    string
		machine ProxmoxMachine
		want    string
	}{
		{
			"domain zone wins over network zone",
			ProxmoxMachine{Spec: ProxmoxMachineSpec{
				FailureDomain: "rack-1",
				Network:       &NetworkSpec{Zone: ptr.To("legacy")},
			}},
			"dmz",
		},
		{
			"domain without zone falls back to its name",
			ProxmoxMachine{Spec: ProxmoxMachineSpec{FailureDomain: "rack-2"}},
			"rack-2",
		},
		{
			"no domain uses the deprecated network zone",
			ProxmoxMachine{Spec: ProxmoxMachineSpec{Network: &NetworkSpec{Zone: ptr.To("legacy")}}},
			"legacy",
		},
		{
			"unknown domain uses the deprecated network zone",
			ProxmoxMachine{Spec: ProxmoxMachineSpec{
				FailureDomain: "rack-9",
				Network:       &NetworkSpec{Zone: ptr.To("legacy")},
			}},
			"legacy",
		},
		{
			"nothing set is the default zone",
			ProxmoxMachine{},
			"default",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, cluster.ZoneForMachine(&test.machine))
		})
	}
}

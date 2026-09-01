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

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"

	infrav1 "github.com/ionos-cloud/cluster-api-provider-proxmox/api/v1alpha2"
)

func TestReconcileFailureDomains(t *testing.T) {
	ipv4 := &infrav1.IPConfigSpec{Addresses: []string{"10.0.0.2-10.0.0.10"}}

	tests := []struct {
		name            string
		spec            infrav1.ProxmoxClusterSpec
		want            []clusterv1.FailureDomain
		wantCondition   *metav1.ConditionStatus
		conditionReason string
	}{
		{
			name: "no domains declared",
			spec: infrav1.ProxmoxClusterSpec{IPv4Config: ipv4},
			want: nil,
		},
		{
			name: "domains on the implicit default zone",
			spec: infrav1.ProxmoxClusterSpec{
				IPv4Config: ipv4,
				FailureDomains: []infrav1.ProxmoxFailureDomain{
					{Name: "default", Nodes: []string{"pve1"}},
				},
			},
			want: []clusterv1.FailureDomain{
				{Name: "default"},
			},
			wantCondition:   ptr.To(metav1.ConditionTrue),
			conditionReason: infrav1.ProxmoxClusterFailureDomainsPublishedReason,
		},
		{
			name: "control plane flag is carried through",
			spec: infrav1.ProxmoxClusterSpec{
				IPv4Config:  ipv4,
				ZoneConfigs: []infrav1.ZoneConfigSpec{{Zone: ptr.To("dmz")}},
				FailureDomains: []infrav1.ProxmoxFailureDomain{
					{Name: "rack-1", Nodes: []string{"pve1"}, Zone: "dmz", ControlPlane: ptr.To(true)},
					{Name: "rack-2", Nodes: []string{"pve2"}, Zone: "dmz", ControlPlane: ptr.To(false)},
				},
			},
			want: []clusterv1.FailureDomain{
				{Name: "rack-1", ControlPlane: ptr.To(true)},
				{Name: "rack-2", ControlPlane: ptr.To(false)},
			},
			wantCondition:   ptr.To(metav1.ConditionTrue),
			conditionReason: infrav1.ProxmoxClusterFailureDomainsPublishedReason,
		},
		{
			name: "domain naming an unknown zone is excluded",
			spec: infrav1.ProxmoxClusterSpec{
				IPv4Config:  ipv4,
				ZoneConfigs: []infrav1.ZoneConfigSpec{{Zone: ptr.To("dmz")}},
				FailureDomains: []infrav1.ProxmoxFailureDomain{
					{Name: "rack-1", Nodes: []string{"pve1"}, Zone: "dmz"},
					{Name: "rack-2", Nodes: []string{"pve2"}, Zone: "nowhere"},
				},
			},
			want: []clusterv1.FailureDomain{
				{Name: "rack-1"},
			},
			wantCondition:   ptr.To(metav1.ConditionFalse),
			conditionReason: infrav1.ProxmoxClusterFailureDomainsPublishedInvalidZoneReason,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cluster := &infrav1.ProxmoxCluster{Spec: test.spec}

			reconcileFailureDomains(cluster)

			require.Equal(t, test.want, cluster.Status.FailureDomains)

			condition := conditions.Get(cluster, infrav1.ProxmoxClusterFailureDomainsPublishedCondition)
			if test.wantCondition == nil {
				require.Nil(t, condition)

				return
			}

			require.NotNil(t, condition)
			require.Equal(t, *test.wantCondition, condition.Status)
			require.Equal(t, test.conditionReason, condition.Reason)
		})
	}
}

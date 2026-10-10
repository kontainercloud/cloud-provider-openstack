/*
Copyright 2026 The Kubernetes Authors.

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

package openstack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/proto"

	lbv1 "k8s.io/cloud-provider-openstack/pkg/util/lbapi/gen"
)

// configHashVersion is part of the hash input. Changing what goes into the
// hash must change it, so that every load balancer is updated exactly once.
const configHashVersion = "v1"

// The hash is computed over these types rather than over the generated proto
// messages: a field added to the proto would otherwise change every hash and
// update every load balancer.
type hashedConfig struct {
	Version               string           `json:"version"`
	Listeners             []hashedListener `json:"listeners"`
	ClientConnLimit       int32            `json:"clientConnLimit"`
	SecurityGroupDisabled bool             `json:"securityGroupDisabled"`
	SecurityGroupIDs      []string         `json:"securityGroupIDs"`
	QoSPolicyDisabled     bool             `json:"qosPolicyDisabled"`
	QoSPolicyID           string           `json:"qosPolicyID"`
	VPCCIDR               string           `json:"vpcCIDR"`
}

type hashedListener struct {
	Port      int32        `json:"port"`
	Protocol  string       `json:"protocol"`
	Hostnames []string     `json:"hostnames"`
	Rules     []hashedRule `json:"rules"`
}

type hashedRule struct {
	Matches       []string        `json:"matches"`
	Algorithm     string          `json:"algorithm"`
	HealthMonitor *hashedMonitor  `json:"healthMonitor"`
	Backends      []hashedBackend `json:"backends"`
}

type hashedMonitor struct {
	Interval            int32   `json:"interval"`
	Timeout             int32   `json:"timeout"`
	HealthyThreshold    int32   `json:"healthyThreshold"`
	UnhealthyThreshold  int32   `json:"unhealthyThreshold"`
	HTTPPath            string  `json:"httpPath"`
	HTTPMethod          string  `json:"httpMethod"`
	ExpectedStatusCodes []int32 `json:"expectedStatusCodes"`
}

type hashedBackend struct {
	Name      string   `json:"name"`
	Weight    int32    `json:"weight"`
	Endpoints []string `json:"endpoints"`
}

// configHash fingerprints the part of a load balancer's configuration that
// UpdateLoadBalancer can change. Server-assigned IDs and the tenant, network
// and subnet, which cannot change after creation, are left out.
func configHash(spec *lbv1.LoadBalancerSpec) string {
	cfg := hashedConfig{
		Version:               configHashVersion,
		ClientConnLimit:       spec.GetClientConnLimit(),
		SecurityGroupDisabled: spec.GetSecurityGroupDisabled(),
		SecurityGroupIDs:      spec.GetSecurityGroupIds(),
		QoSPolicyDisabled:     spec.GetQosPolicyDisabled(),
		QoSPolicyID:           spec.GetQosPolicyId(),
		VPCCIDR:               spec.GetVpcCidr(),
	}
	for _, l := range spec.GetListeners() {
		hl := hashedListener{Port: l.GetPort(), Protocol: l.GetProtocol().String(), Hostnames: l.GetHostnames()}
		for _, r := range l.GetRules() {
			hr := hashedRule{Algorithm: r.GetAlgorithm().String()}
			for _, m := range r.GetMatches() {
				// Matches are compared as their wire form; they carry no IDs.
				b, _ := proto.MarshalOptions{Deterministic: true}.Marshal(m)
				hr.Matches = append(hr.Matches, hex.EncodeToString(b))
			}
			if hm := r.GetHealthMonitor(); hm != nil {
				hr.HealthMonitor = &hashedMonitor{
					Interval:            hm.GetInterval(),
					Timeout:             hm.GetTimeout(),
					HealthyThreshold:    hm.GetHealthyThreshold(),
					UnhealthyThreshold:  hm.GetUnhealthyThreshold(),
					HTTPPath:            hm.GetHttpHealthCheckPath(),
					HTTPMethod:          hm.GetHttpHealthCheckMethod().String(),
					ExpectedStatusCodes: hm.GetExpectedStatusCodes(),
				}
			}
			for _, b := range r.GetBackends() {
				hb := hashedBackend{Name: b.GetName(), Weight: b.GetWeight()}
				for _, e := range b.GetEndpoints() {
					hb.Endpoints = append(hb.Endpoints, fmt.Sprintf("%s:%d", e.GetIp(), e.GetPort()))
				}
				hr.Backends = append(hr.Backends, hb)
			}
			hl.Rules = append(hl.Rules, hr)
		}
		cfg.Listeners = append(cfg.Listeners, hl)
	}

	data, _ := json.Marshal(cfg)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// mergeListenerIDs returns desired with the server-assigned identifiers found
// in current grafted on, matching listeners by port and protocol.
//
// The service reconciles an update by ID alone: a listener sent without one is
// inserted fresh and the one it replaces - with its rules, its backends and
// the data plane objects generated from them - is deleted. Node changes update
// the load balancer, so dropping the IDs would rebuild every listener each
// time the endpoint list moves.
//
// desired is left untouched; the IDs are grafted onto clones.
func mergeListenerIDs(desired, current []*lbv1.Listener) []*lbv1.Listener {
	existing := make(map[string]*lbv1.Listener, len(current))
	for _, l := range current {
		existing[listenerKey(l)] = l
	}

	merged := make([]*lbv1.Listener, 0, len(desired))
	for _, want := range desired {
		clone := proto.Clone(want).(*lbv1.Listener)
		merged = append(merged, clone)

		have := existing[listenerKey(clone)]
		if have == nil {
			continue
		}
		clone.Id = have.GetId()

		// One rule with one backend on both sides, so position is identity.
		// Anything else was not made by this controller and is left to the
		// service's own reconciliation.
		if len(clone.GetRules()) != 1 || len(have.GetRules()) != 1 {
			continue
		}
		wantRule, haveRule := clone.GetRules()[0], have.GetRules()[0]
		wantRule.Id = haveRule.GetId()

		if len(wantRule.GetBackends()) != 1 || len(haveRule.GetBackends()) != 1 {
			continue
		}
		wantRule.GetBackends()[0].Id = haveRule.GetBackends()[0].GetId()
	}

	return merged
}

func listenerKey(l *lbv1.Listener) string {
	return fmt.Sprintf("%d/%s", l.GetPort(), l.GetProtocol())
}

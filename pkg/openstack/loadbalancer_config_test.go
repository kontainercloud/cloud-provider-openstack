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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/cloud-provider-openstack/pkg/util/lbapi"
)

const testGlobalConfig = `
 [Global]
 auth-url = http://auth.url
 user-id = user
 password = mypass
 tenant-name = demo
 region = RegionOne
`

func TestReadConfigLoadBalancerService(t *testing.T) {
	cfg, err := ReadConfig(strings.NewReader(testGlobalConfig + `
 [LoadBalancer]
 rpc-server-addr = http://loadbalancer-api.lb-system.svc:8080
 api-key = s3cret
 rpc-timeout = 45s
 rpc-retry-max = 5
 tenant-id = 0f1e2d3c4b5a69788796a5b4c3d2e1f0
 vpc-cidr = 10.0.0.0/16
 security-group-ids = 55555555-5555-4555-8555-555555555555,66666666-6666-4666-8666-666666666666
 qos-policy-id = 77777777-7777-4777-8777-777777777777
 subnet-id = 22222222-2222-4222-8222-222222222222
 network-id = 11111111-1111-4111-8111-111111111111
 floating-network-id = 33333333-3333-4333-8333-333333333333
`))
	require.NoError(t, err)

	lb := cfg.LoadBalancer
	assert.Equal(t, "http://loadbalancer-api.lb-system.svc:8080", lb.RPCServerAddr)
	assert.Equal(t, "s3cret", lb.APIKey)
	assert.Equal(t, 45*time.Second, lb.RPCTimeout.Duration)
	assert.Equal(t, 5, lb.RPCRetryMax)
	assert.Equal(t, "0f1e2d3c4b5a69788796a5b4c3d2e1f0", lb.TenantID)
	assert.Equal(t, "10.0.0.0/16", lb.VPCCIDR)
	assert.Equal(t, "55555555-5555-4555-8555-555555555555,66666666-6666-4666-8666-666666666666", lb.SecurityGroupIDs)
	assert.Equal(t, "77777777-7777-4777-8777-777777777777", lb.QoSPolicyID)
	assert.Equal(t, "22222222-2222-4222-8222-222222222222", lb.SubnetID)
	assert.Equal(t, "11111111-1111-4111-8111-111111111111", lb.NetworkID)
	assert.Equal(t, "33333333-3333-4333-8333-333333333333", lb.FloatingNetworkID)
	assert.Empty(t, ignoredLoadBalancerOpts(cfg))
}

func TestReadConfigLoadBalancerDefaults(t *testing.T) {
	cfg, err := ReadConfig(strings.NewReader(testGlobalConfig))
	require.NoError(t, err)

	lb := cfg.LoadBalancer
	assert.True(t, lb.Enabled)
	assert.Empty(t, lb.RPCServerAddr)
	assert.Empty(t, lb.APIKey, "no API key means no credential is sent")
	assert.Equal(t, lbapi.DefaultTimeout, lb.RPCTimeout.Duration)
	assert.Equal(t, lbapi.DefaultRetryMax, lb.RPCRetryMax)
	assert.True(t, lb.CreateMonitor, "health monitors are on by default")
	assert.Equal(t, 5*time.Second, lb.MonitorDelay.Duration)
	assert.Equal(t, 3*time.Second, lb.MonitorTimeout.Duration)
	assert.Equal(t, "ROUND_ROBIN", lb.LBMethod)
	assert.Empty(t, ignoredLoadBalancerOpts(cfg))
}

func TestIgnoredLoadBalancerOpts(t *testing.T) {
	cfg, err := ReadConfig(strings.NewReader(testGlobalConfig + `
 [LoadBalancer]
 rpc-server-addr = loadbalancer-api:8080
 lb-provider = ovn
 lb-version = v2
 flavor-id = some-flavor
 max-shared-lb = 5
 cascade-delete = false
 floating-subnet-id = 88888888-8888-4888-8888-888888888888
 [LoadBalancerClass "internet"]
 floating-network-id = 33333333-3333-4333-8333-333333333333
`))
	require.NoError(t, err)

	assert.Equal(t, []string{
		"LoadBalancerClass sections",
		"cascade-delete",
		"flavor-id",
		"floating-subnet-id",
		"lb-provider",
		"lb-version",
		"max-shared-lb",
	}, ignoredLoadBalancerOpts(cfg))
}

func TestProjectIDFromProvider(t *testing.T) {
	assert.Equal(t, "configured", projectIDFromProvider(nil, "configured"))
	assert.Empty(t, projectIDFromProvider(nil, ""))
}

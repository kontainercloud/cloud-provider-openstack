/*
Copyright 2016 The Kubernetes Authors.

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
	"context"
	"fmt"
	"net"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog/v2"
	netutils "k8s.io/utils/net"
	"k8s.io/utils/ptr"

	"k8s.io/cloud-provider-openstack/pkg/metrics"
	cpoutil "k8s.io/cloud-provider-openstack/pkg/util"
	cpoerrors "k8s.io/cloud-provider-openstack/pkg/util/errors"
	"k8s.io/cloud-provider-openstack/pkg/util/lbapi"
	lbv1 "k8s.io/cloud-provider-openstack/pkg/util/lbapi/gen"
)

const (
	ServiceAnnotationLoadBalancerInternal             = "service.beta.kubernetes.io/openstack-internal-load-balancer"
	ServiceAnnotationLoadBalancerNodeSelector         = "loadbalancer.openstack.org/node-selector"
	ServiceAnnotationLoadBalancerConnLimit            = "loadbalancer.openstack.org/connection-limit"
	ServiceAnnotationLoadBalancerFloatingNetworkID    = "loadbalancer.openstack.org/floating-network-id"
	ServiceAnnotationLoadBalancerFloatingSubnet       = "loadbalancer.openstack.org/floating-subnet"
	ServiceAnnotationLoadBalancerFloatingSubnetID     = "loadbalancer.openstack.org/floating-subnet-id"
	ServiceAnnotationLoadBalancerFloatingSubnetTags   = "loadbalancer.openstack.org/floating-subnet-tags"
	ServiceAnnotationLoadBalancerClass                = "loadbalancer.openstack.org/class"
	ServiceAnnotationLoadBalancerKeepFloatingIP       = "loadbalancer.openstack.org/keep-floatingip"
	ServiceAnnotationLoadBalancerPortID               = "loadbalancer.openstack.org/port-id"
	ServiceAnnotationLoadBalancerLbMethod             = "loadbalancer.openstack.org/lb-method"
	ServiceAnnotationLoadBalancerProxyEnabled         = "loadbalancer.openstack.org/proxy-protocol"
	ServiceAnnotationLoadBalancerSubnetID             = "loadbalancer.openstack.org/subnet-id"
	ServiceAnnotationLoadBalancerNetworkID            = "loadbalancer.openstack.org/network-id"
	ServiceAnnotationLoadBalancerMemberSubnetID       = "loadbalancer.openstack.org/member-subnet-id"
	ServiceAnnotationLoadBalancerTimeoutClientData    = "loadbalancer.openstack.org/timeout-client-data"
	ServiceAnnotationLoadBalancerTimeoutMemberConnect = "loadbalancer.openstack.org/timeout-member-connect"
	ServiceAnnotationLoadBalancerTimeoutMemberData    = "loadbalancer.openstack.org/timeout-member-data"
	ServiceAnnotationLoadBalancerTimeoutTCPInspect    = "loadbalancer.openstack.org/timeout-tcp-inspect"
	ServiceAnnotationLoadBalancerXForwardedFor        = "loadbalancer.openstack.org/x-forwarded-for"
	ServiceAnnotationLoadBalancerFlavorID             = "loadbalancer.openstack.org/flavor-id"
	ServiceAnnotationLoadBalancerAvailabilityZone     = "loadbalancer.openstack.org/availability-zone"
	// ServiceAnnotationLoadBalancerEnableHealthMonitor defines whether to attach a health monitor to the load
	// balancer's TCP listeners, if not specified, use 'create-monitor' config.
	ServiceAnnotationLoadBalancerEnableHealthMonitor         = "loadbalancer.openstack.org/enable-health-monitor"
	ServiceAnnotationLoadBalancerHealthMonitorDelay          = "loadbalancer.openstack.org/health-monitor-delay"
	ServiceAnnotationLoadBalancerHealthMonitorTimeout        = "loadbalancer.openstack.org/health-monitor-timeout"
	ServiceAnnotationLoadBalancerHealthMonitorMaxRetries     = "loadbalancer.openstack.org/health-monitor-max-retries"
	ServiceAnnotationLoadBalancerHealthMonitorMaxRetriesDown = "loadbalancer.openstack.org/health-monitor-max-retries-down"
	ServiceAnnotationLoadBalancerLoadbalancerHostname        = "loadbalancer.openstack.org/hostname"
	// ServiceAnnotationLoadBalancerHTTPPath turns every TCP port of the Service into an HTTP listener. The value is a
	// path prefix the listener matches; "/" matches every request.
	ServiceAnnotationLoadBalancerHTTPPath = "loadbalancer.openstack.org/http-path"
	// ServiceAnnotationLoadBalancerHTTPMethod restricts the HTTP listeners to one request method.
	ServiceAnnotationLoadBalancerHTTPMethod = "loadbalancer.openstack.org/http-method"
	// ServiceAnnotationLoadBalancerHTTPHostnames restricts the HTTP listeners to the given Host headers (comma-separated).
	ServiceAnnotationLoadBalancerHTTPHostnames = "loadbalancer.openstack.org/http-hostnames"
	// ServiceAnnotationLoadBalancerHealthMonitorHTTPPath is the path the health monitor of an HTTP listener requests.
	// Defaults to the http-path annotation.
	ServiceAnnotationLoadBalancerHealthMonitorHTTPPath = "loadbalancer.openstack.org/health-monitor-http-path"
	// ServiceAnnotationLoadBalancerHealthMonitorHTTPMethod is the method of the HTTP health check. Defaults to GET.
	ServiceAnnotationLoadBalancerHealthMonitorHTTPMethod = "loadbalancer.openstack.org/health-monitor-http-method"
	// ServiceAnnotationLoadBalancerHealthMonitorExpectedCodes lists the status codes of a healthy backend
	// (comma-separated). Defaults to 200.
	ServiceAnnotationLoadBalancerHealthMonitorExpectedCodes = "loadbalancer.openstack.org/health-monitor-expected-codes"
	ServiceAnnotationLoadBalancerAddress                    = "loadbalancer.openstack.org/load-balancer-address"
	// revive:disable:var-naming
	ServiceAnnotationTlsContainerRef = "loadbalancer.openstack.org/default-tls-container-ref"
	// revive:enable:var-naming
	// See https://nip.io
	defaultProxyHostnameSuffix = "nip.io"
	// ServiceAnnotationLoadBalancerID holds the ID the load balancer service assigned to the Service's load
	// balancer. It is written by the controller.
	ServiceAnnotationLoadBalancerID = "loadbalancer.openstack.org/load-balancer-id"
	// ServiceAnnotationLoadBalancerCreateAttempt counts the creations of the Service's load balancer. It feeds
	// the idempotency key of the create request and is written by the controller.
	ServiceAnnotationLoadBalancerCreateAttempt = "loadbalancer.openstack.org/create-attempt"
	// ServiceAnnotationLoadBalancerConfigHash fingerprints the configuration the load balancer service last
	// accepted for the Service. An update is only sent when it changes. It is written by the controller.
	ServiceAnnotationLoadBalancerConfigHash = "loadbalancer.openstack.org/config-hash"

	// ServiceAnnotationLoadBalancerTags is not supported by the load balancer service.
	ServiceAnnotationLoadBalancerTags = "loadbalancer.openstack.org/load-balancer-tags"
	// ServiceAnnotationListenerTags is not supported by the load balancer service.
	ServiceAnnotationListenerTags = "loadbalancer.openstack.org/listener-tags"
	// ServiceAnnotationPoolTags is not supported by the load balancer service.
	ServiceAnnotationPoolTags = "loadbalancer.openstack.org/pool-tags"

	// Load balancer name format
	servicePrefix = "kube_service_"
	lbFormat      = "%s%s_%s_%s"

	// Every listener carries exactly one rule with exactly one backend, so the
	// backend needs no distinguishing name. The weight has to be set explicitly:
	// the API rejects a rule whose backends all have weight 0.
	defaultBackendName   = "default"
	defaultBackendWeight = int32(1)

	// The API caps a page at 200 and rejects anything below 1.
	listLoadBalancersPageSize = int32(200)

	// waitLoadBalancerActiveStepsEnv overrides the number of polls made while
	// waiting for a load balancer to become ready.
	waitLoadBalancerActiveStepsEnv = "OCCM_WAIT_LB_ACTIVE_STEPS"
)

// Polling of the load balancer state: 1s initial delay growing by 1.2, which
// is about 4.5 minutes for a create and 5.4 minutes for a delete. Variables
// so that tests can shorten them.
var (
	waitLoadBalancerInitDelay   = 1 * time.Second
	waitLoadBalancerFactor      = 1.2
	waitLoadBalancerActiveSteps = 23
	waitLoadBalancerDeleteSteps = 24
)

// unsupportedServiceAnnotations are annotations of the Octavia implementation
// that have no counterpart in the load balancer service.
var unsupportedServiceAnnotations = []string{
	ServiceAnnotationLoadBalancerClass,
	ServiceAnnotationLoadBalancerPortID,
	ServiceAnnotationLoadBalancerProxyEnabled,
	ServiceAnnotationLoadBalancerXForwardedFor,
	ServiceAnnotationLoadBalancerMemberSubnetID,
	ServiceAnnotationLoadBalancerTimeoutClientData,
	ServiceAnnotationLoadBalancerTimeoutMemberConnect,
	ServiceAnnotationLoadBalancerTimeoutMemberData,
	ServiceAnnotationLoadBalancerTimeoutTCPInspect,
	ServiceAnnotationLoadBalancerFlavorID,
	ServiceAnnotationLoadBalancerAvailabilityZone,
	ServiceAnnotationTlsContainerRef,
	ServiceAnnotationLoadBalancerFloatingSubnet,
	ServiceAnnotationLoadBalancerFloatingSubnetID,
	ServiceAnnotationLoadBalancerFloatingSubnetTags,
	ServiceAnnotationLoadBalancerTags,
	ServiceAnnotationListenerTags,
	ServiceAnnotationPoolTags,
}

// LbaasV2 is a LoadBalancer implementation based on the load balancer service
// (loadbalancer.v1.LoadBalancerService).
type LbaasV2 struct {
	LoadBalancer
}

var _ cloudprovider.LoadBalancer = &LbaasV2{}

// serviceConfig contains configurations for creating a Service.
type serviceConfig struct {
	internal                    bool
	connLimit                   int
	tenantID                    string
	lbNetworkID                 string
	lbSubnetID                  string
	lbMemberSubnetID            string
	lbPublicNetworkID           string
	requestedFloatingIP         string
	nodeSelectors               map[string]string
	algorithm                   lbv1.Algorithm
	enableMonitor               bool
	lbID                        string
	healthMonitorDelay          int
	healthMonitorTimeout        int
	healthMonitorMaxRetries     int
	healthMonitorMaxRetriesDown int
	http                        *httpConfig     // nil when the Service's TCP ports are plain TCP listeners
	preferredIPFamily           corev1.IPFamily // preferred (the first) IP family indicated in service's `spec.ipFamilies`
}

// httpConfig is how the TCP ports of a Service are served when it asks for HTTP listeners.
type httpConfig struct {
	pathPrefix      string
	method          lbv1.HttpMethod
	hostnames       []string
	monitorPath     string
	monitorMethod   lbv1.HttpMethod
	monitorExpected []int32
}

// hostnamePattern is the hostname the load balancer service accepts on a listener.
var hostnamePattern = regexp.MustCompile(`^(\*\.)?([a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}$`)

func parseHTTPMethod(method string) (lbv1.HttpMethod, error) {
	if v, ok := lbv1.HttpMethod_value["HTTP_METHOD_"+strings.ToUpper(strings.TrimSpace(method))]; ok && v != 0 {
		return lbv1.HttpMethod(v), nil
	}
	return lbv1.HttpMethod_HTTP_METHOD_UNSPECIFIED, fmt.Errorf("unknown HTTP method %q", method)
}

// getHTTPConfig reads the HTTP annotations of the Service; nil means none were set.
func getHTTPConfig(service *corev1.Service) (*httpConfig, error) {
	path, ok := service.Annotations[ServiceAnnotationLoadBalancerHTTPPath]
	if !ok {
		for _, annotation := range []string{ServiceAnnotationLoadBalancerHTTPMethod, ServiceAnnotationLoadBalancerHTTPHostnames} {
			if _, set := service.Annotations[annotation]; set {
				return nil, fmt.Errorf("annotation %s needs %s, which turns the TCP ports into HTTP listeners", annotation, ServiceAnnotationLoadBalancerHTTPPath)
			}
		}
		return nil, nil
	}

	path = strings.TrimSpace(path)
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("annotation %s must start with /, got %q", ServiceAnnotationLoadBalancerHTTPPath, path)
	}
	conf := &httpConfig{
		pathPrefix:      path,
		monitorPath:     getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerHealthMonitorHTTPPath, path),
		monitorMethod:   lbv1.HttpMethod_HTTP_METHOD_GET,
		monitorExpected: []int32{200},
	}
	if !strings.HasPrefix(conf.monitorPath, "/") {
		return nil, fmt.Errorf("annotation %s must start with /, got %q", ServiceAnnotationLoadBalancerHealthMonitorHTTPPath, conf.monitorPath)
	}

	if method := getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerHTTPMethod, ""); method != "" {
		m, err := parseHTTPMethod(method)
		if err != nil {
			return nil, fmt.Errorf("annotation %s: %v", ServiceAnnotationLoadBalancerHTTPMethod, err)
		}
		conf.method = m
	}
	if method := getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerHealthMonitorHTTPMethod, ""); method != "" {
		m, err := parseHTTPMethod(method)
		if err != nil {
			return nil, fmt.Errorf("annotation %s: %v", ServiceAnnotationLoadBalancerHealthMonitorHTTPMethod, err)
		}
		conf.monitorMethod = m
	}

	for _, hostname := range cpoutil.SplitTrim(getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerHTTPHostnames, ""), ',') {
		if !hostnamePattern.MatchString(hostname) {
			return nil, fmt.Errorf("annotation %s: %q is not a valid hostname", ServiceAnnotationLoadBalancerHTTPHostnames, hostname)
		}
		if !slices.Contains(conf.hostnames, hostname) {
			conf.hostnames = append(conf.hostnames, hostname)
		}
	}
	if len(conf.hostnames) > 16 {
		return nil, fmt.Errorf("annotation %s lists %d hostnames, at most 16 are allowed", ServiceAnnotationLoadBalancerHTTPHostnames, len(conf.hostnames))
	}

	if codes := getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerHealthMonitorExpectedCodes, ""); codes != "" {
		conf.monitorExpected = nil
		for _, code := range cpoutil.SplitTrim(codes, ',') {
			c, err := strconv.Atoi(code)
			if err != nil || c < 100 || c > 599 {
				return nil, fmt.Errorf("annotation %s: %q is not an HTTP status code", ServiceAnnotationLoadBalancerHealthMonitorExpectedCodes, code)
			}
			conf.monitorExpected = append(conf.monitorExpected, int32(c))
		}
	}

	return conf, nil
}

// isLoadBalancerGone tells whether the load balancer is absent or on its way out.
func isLoadBalancerGone(lb *lbv1.LoadBalancer) bool {
	if lb == nil {
		return true
	}
	return lb.GetState() == lbv1.State_STATE_DELETING || lb.GetState() == lbv1.State_STATE_DELETED
}

// loadBalancerStateDetail renders the state and, when the service reported one, the error of a load balancer.
func loadBalancerStateDetail(lb *lbv1.LoadBalancer) string {
	if lb.GetError() != "" {
		return fmt.Sprintf("%s: %s", lb.GetState(), lb.GetError())
	}
	return lb.GetState().String()
}

// getLoadbalancerByID gets the load balancer by its ID. A load balancer that
// does not exist yields cpoerrors.ErrNotFound; one that is being deleted is
// returned as is.
func getLoadbalancerByID(ctx context.Context, client lbapi.Interface, id string) (*lbv1.LoadBalancer, error) {
	mc := metrics.NewMetricContext("loadbalancer", "get")
	resp, err := client.GetLoadBalancer(ctx, &lbv1.GetLoadBalancerRequest{Id: id})
	if lbapi.IsNotFound(err) {
		_ = mc.ObserveRequest(nil)
		return nil, cpoerrors.ErrNotFound
	}
	if mc.ObserveRequest(err) != nil {
		return nil, err
	}
	if resp.GetLoadBalancer() == nil {
		return nil, cpoerrors.ErrNotFound
	}
	return resp.GetLoadBalancer(), nil
}

// getLoadbalancerByName gets the load balancer of the tenant which is in valid
// status by the given name. The service has no filter by name, so the
// tenant's load balancers are paged through and matched here.
func getLoadbalancerByName(ctx context.Context, client lbapi.Interface, tenantID, name string) (*lbv1.LoadBalancer, error) {
	var ids []string

	pageToken := ""
	for {
		mc := metrics.NewMetricContext("loadbalancer", "list")
		resp, err := client.ListLoadBalancers(ctx, &lbv1.ListLoadBalancersRequest{
			TenantId:  tenantID,
			PageSize:  listLoadBalancersPageSize,
			PageToken: pageToken,
		})
		if mc.ObserveRequest(err) != nil {
			return nil, err
		}

		for _, summary := range resp.GetLoadBalancers() {
			if summary.GetName() != name {
				continue
			}
			// The list leaves out deleted load balancers but not the ones being deleted.
			if summary.GetState() == lbv1.State_STATE_DELETING || summary.GetState() == lbv1.State_STATE_DELETED {
				continue
			}
			ids = append(ids, summary.GetId())
		}

		pageToken = resp.GetNextPageToken()
		if pageToken == "" {
			break
		}
	}

	if len(ids) > 1 {
		return nil, cpoerrors.ErrMultipleResults
	}
	if len(ids) == 0 {
		return nil, cpoerrors.ErrNotFound
	}

	return getLoadbalancerByID(ctx, client, ids[0])
}

// waitLoadBalancerBackoff returns the polling schedule for a load balancer state change.
func waitLoadBalancerBackoff(steps int) wait.Backoff {
	return wait.Backoff{
		Duration: waitLoadBalancerInitDelay,
		Factor:   waitLoadBalancerFactor,
		Steps:    steps,
	}
}

func getWaitSteps(name string, steps int) int {
	if v := os.Getenv(name); v != "" {
		s, err := strconv.Atoi(v)
		if err == nil && s >= 0 {
			return s
		}
	}
	return steps
}

// isLoadBalancerReady tells whether the load balancer is provisioned and has
// its addresses: the VIP, and the floating IP when one was requested.
func isLoadBalancerReady(lb *lbv1.LoadBalancer) bool {
	if lb.GetState() != lbv1.State_STATE_READY || lb.GetIp() == "" {
		return false
	}
	if lb.GetFipEnabled() {
		return lb.GetFipState() == lbv1.FipState_FIP_STATE_ACTIVE && lb.GetFip() != ""
	}
	return true
}

// waitLoadBalancerReady waits for the load balancer to be ready then returns it for further usage.
func (lbaas *LbaasV2) waitLoadBalancerReady(ctx context.Context, lbID string) (*lbv1.LoadBalancer, error) {
	klog.InfoS("Waiting for load balancer READY", "lbID", lbID)
	backoff := waitLoadBalancerBackoff(getWaitSteps(waitLoadBalancerActiveStepsEnv, waitLoadBalancerActiveSteps))

	var loadbalancer *lbv1.LoadBalancer
	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		var err error
		loadbalancer, err = getLoadbalancerByID(ctx, lbaas.lb, lbID)
		if err != nil {
			if lbapi.IsRetryable(err) {
				klog.Warningf("Failed to get load balancer %s while waiting for it, will retry: %v", lbID, err)
				return false, nil
			}
			return false, err
		}
		if isLoadBalancerGone(loadbalancer) {
			return false, fmt.Errorf("load balancer %s is being deleted", lbID)
		}
		// A failed load balancer does not recover within this wait; waiting out
		// the rest only hides the reason the service reported.
		if loadbalancer.GetState() == lbv1.State_STATE_FAILED {
			return false, fmt.Errorf("load balancer %s failed to provision, %s", lbID, loadBalancerStateDetail(loadbalancer))
		}
		return isLoadBalancerReady(loadbalancer), nil
	})
	if wait.Interrupted(err) {
		err = fmt.Errorf("timeout waiting for the load balancer %s to be READY, current state %s", lbID, loadBalancerStateDetail(loadbalancer))
	}

	return loadbalancer, err
}

// waitLoadBalancerDeleted waits until the load balancer is gone.
func (lbaas *LbaasV2) waitLoadBalancerDeleted(ctx context.Context, lbID string) error {
	klog.V(4).InfoS("Waiting for load balancer deleted", "lbID", lbID)
	backoff := waitLoadBalancerBackoff(waitLoadBalancerDeleteSteps)

	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		loadbalancer, err := getLoadbalancerByID(ctx, lbaas.lb, lbID)
		if err != nil {
			if cpoerrors.IsNotFound(err) {
				return true, nil
			}
			if lbapi.IsRetryable(err) {
				return false, nil
			}
			return false, err
		}
		// The controller removes the row shortly after it marked it deleted.
		return loadbalancer.GetState() == lbv1.State_STATE_DELETED, nil
	})
	if wait.Interrupted(err) {
		err = fmt.Errorf("loadbalancer %s failed to delete within the allotted time", lbID)
	}

	return err
}

// createIdempotencyKey derives the key for one creation attempt. It must be
// stable across retries of the same attempt, so a lost response replays instead
// of leaking a second load balancer - and it must differ between attempts, or
// the service replays the previous (already deleted) creation. The key has to
// be a UUID per the API contract.
func createIdempotencyKey(service *corev1.Service, attempt int) string {
	space, err := uuid.Parse(string(service.UID))
	if err != nil {
		return uuid.NewSHA1(uuid.Nil, fmt.Appendf(nil, "%s/%d", service.UID, attempt)).String()
	}
	if attempt <= 1 {
		return space.String()
	}
	return uuid.NewSHA1(space, []byte(strconv.Itoa(attempt))).String()
}

// getCreateAttempt returns the number of the current creation of the Service's load balancer.
func getCreateAttempt(service *corev1.Service) int {
	attempt := getIntFromServiceAnnotation(service, ServiceAnnotationLoadBalancerCreateAttempt, 1)
	if attempt < 1 {
		return 1
	}
	return attempt
}

// listenerProtocol picks the listener protocol for one Service port.
func listenerProtocol(port corev1.ServicePort) (lbv1.Protocol, error) {
	switch port.Protocol {
	case corev1.ProtocolTCP, "":
		return lbv1.Protocol_PROTOCOL_TCP, nil
	case corev1.ProtocolUDP:
		return lbv1.Protocol_PROTOCOL_UDP, nil
	default:
		return lbv1.Protocol_PROTOCOL_UNSPECIFIED, fmt.Errorf("protocol %s of port %d is not supported, only TCP and UDP are", port.Protocol, port.Port)
	}
}

// parseAlgorithm maps the lb-method setting to the algorithm of the load balancer service.
func parseAlgorithm(lbMethod string) (lbv1.Algorithm, error) {
	switch strings.ToUpper(strings.TrimSpace(lbMethod)) {
	case "", "ROUND_ROBIN":
		return lbv1.Algorithm_ALGORITHM_ROUND_ROBIN, nil
	case "LEAST_CONNECTIONS":
		return lbv1.Algorithm_ALGORITHM_LEAST_REQUEST, nil
	case "SOURCE_IP", "SOURCE_IP_PORT":
		return lbv1.Algorithm_ALGORITHM_CONSISTENT_HASH, nil
	default:
		return lbv1.Algorithm_ALGORITHM_UNSPECIFIED, fmt.Errorf("unknown lb-method %q, supported are ROUND_ROBIN, LEAST_CONNECTIONS, SOURCE_IP and SOURCE_IP_PORT", lbMethod)
	}
}

// isUsableNodeIP rules out addresses the load balancer cannot route to. An
// unroutable endpoint is not merely useless: it makes the whole load balancer
// fail to provision.
func isUsableNodeIP(address string) bool {
	ip := net.ParseIP(address)
	if ip == nil {
		return false
	}
	return !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast()
}

// buildEndpoints resolves the upstream addresses for one Service port: the
// address of every node paired with the node port. The result is deduplicated
// and ordered: the API rejects a backend that lists the same ip:port twice, and
// a stable order keeps the configuration from changing just because the node
// list came back in a different sequence.
func buildEndpoints(port corev1.ServicePort, nodes []*corev1.Node, svcConf *serviceConfig) []*lbv1.BackendRef {
	seen := make(map[string]struct{}, len(nodes))
	endpoints := make([]*lbv1.BackendRef, 0, len(nodes))
	for _, node := range nodes {
		addr, err := nodeAddressForLB(node, svcConf.preferredIPFamily)
		if err != nil {
			// Node failure, do not create member
			klog.Warningf("Failed to get the address of node %s for creating member of load balancer port %d: %v", node.Name, port.Port, err)
			continue
		}
		if !isUsableNodeIP(addr) {
			klog.Warningf("Skipping node %s for load balancer port %d: address %q is not routable", node.Name, port.Port, addr)
			continue
		}
		if _, dup := seen[addr]; dup {
			continue
		}
		seen[addr] = struct{}{}
		endpoints = append(endpoints, &lbv1.BackendRef{Ip: addr, Port: port.NodePort})
	}

	sort.Slice(endpoints, func(i, j int) bool {
		return endpoints[i].GetIp() < endpoints[j].GetIp()
	})

	return endpoints
}

// buildHealthMonitor returns the health monitor of one listener rule, or nil
// when the rule gets none. UDP listeners never get one: the load balancer
// probes with a TCP connect, which a UDP node port does not answer.
func buildHealthMonitor(protocol lbv1.Protocol, svcConf *serviceConfig) *lbv1.HealthMonitor {
	if !svcConf.enableMonitor || protocol == lbv1.Protocol_PROTOCOL_UDP {
		return nil
	}
	monitor := &lbv1.HealthMonitor{
		Interval:           int32(svcConf.healthMonitorDelay),
		Timeout:            int32(svcConf.healthMonitorTimeout),
		HealthyThreshold:   int32(svcConf.healthMonitorMaxRetries),
		UnhealthyThreshold: int32(svcConf.healthMonitorMaxRetriesDown),
	}
	// The service requires path, method and expected codes on the monitor of an HTTP listener.
	if protocol == lbv1.Protocol_PROTOCOL_HTTP {
		monitor.HttpHealthCheckPath = svcConf.http.monitorPath
		monitor.HttpHealthCheckMethod = svcConf.http.monitorMethod
		monitor.ExpectedStatusCodes = svcConf.http.monitorExpected
	}
	return monitor
}

// buildHTTPMatches returns the match of the rule of an HTTP listener. No match
// at all is how the service spells "every request".
func buildHTTPMatches(conf *httpConfig) []*lbv1.HttpRouteMatch {
	match := &lbv1.HttpRouteMatch{Method: conf.method}
	if conf.pathPrefix != "/" {
		match.Path = &lbv1.HttpPathMatch{Type: lbv1.HttpPathType_HTTP_PATH_TYPE_PREFIX, Value: conf.pathPrefix}
	}
	if match.Path == nil && match.Method == lbv1.HttpMethod_HTTP_METHOD_UNSPECIFIED {
		return nil
	}
	return []*lbv1.HttpRouteMatch{match}
}

// buildListeners renders the Service as one listener per port. Each listener
// carries exactly one rule and each rule exactly one backend, whose endpoints
// are the <node address>:<node port> pairs traffic is spread across.
func buildListeners(service *corev1.Service, nodes []*corev1.Node, svcConf *serviceConfig) ([]*lbv1.Listener, error) {
	listeners := make([]*lbv1.Listener, 0, len(service.Spec.Ports))

	for _, port := range service.Spec.Ports {
		protocol, err := listenerProtocol(port)
		if err != nil {
			return nil, err
		}
		if protocol == lbv1.Protocol_PROTOCOL_TCP && svcConf.http != nil {
			protocol = lbv1.Protocol_PROTOCOL_HTTP
		}
		if port.NodePort == 0 {
			return nil, fmt.Errorf("port %d has no node port; Services without node ports are not supported", port.Port)
		}

		endpoints := buildEndpoints(port, nodes, svcConf)
		if len(endpoints) == 0 {
			return nil, fmt.Errorf("no usable node address found for port %d", port.Port)
		}

		listener := &lbv1.Listener{
			Port:     port.Port,
			Protocol: protocol,
			Rules: []*lbv1.ListenerRule{{
				Algorithm: svcConf.algorithm,
				Backends: []*lbv1.RuleBackend{{
					Name:      defaultBackendName,
					Weight:    defaultBackendWeight,
					Endpoints: endpoints,
				}},
				HealthMonitor: buildHealthMonitor(protocol, svcConf),
			}},
		}
		if protocol == lbv1.Protocol_PROTOCOL_HTTP {
			listener.Hostnames = svcConf.http.hostnames
			listener.Rules[0].Matches = buildHTTPMatches(svcConf.http)
		}
		listeners = append(listeners, listener)
	}

	return listeners, nil
}

// buildLoadBalancerSpec renders the configuration of the Service's load balancer.
func (lbaas *LbaasV2) buildLoadBalancerSpec(service *corev1.Service, nodes []*corev1.Node, svcConf *serviceConfig) (*lbv1.LoadBalancerSpec, error) {
	listeners, err := buildListeners(service, nodes, svcConf)
	if err != nil {
		return nil, err
	}

	securityGroupIDs := cpoutil.SplitTrim(lbaas.opts.SecurityGroupIDs, ',')

	return &lbv1.LoadBalancerSpec{
		TenantId:              svcConf.tenantID,
		NetworkId:             svcConf.lbNetworkID,
		SubnetId:              svcConf.lbSubnetID,
		Listeners:             listeners,
		ClientConnLimit:       int32(svcConf.connLimit),
		SecurityGroupDisabled: len(securityGroupIDs) == 0,
		SecurityGroupIds:      securityGroupIDs,
		QosPolicyDisabled:     lbaas.opts.QoSPolicyID == "",
		QosPolicyId:           lbaas.opts.QoSPolicyID,
		VpcCidr:               lbaas.opts.VPCCIDR,
	}, nil
}

// createLoadBalancer creates the fully populated load balancer of the Service
// and waits for it to be ready.
func (lbaas *LbaasV2) createLoadBalancer(ctx context.Context, name string, service *corev1.Service, spec *lbv1.LoadBalancerSpec, svcConf *serviceConfig) (*lbv1.LoadBalancer, error) {
	attempt := getCreateAttempt(service)
	createReq := &lbv1.CreateLoadBalancerRequest{
		Name:           name,
		Spec:           spec,
		IdempotencyKey: createIdempotencyKey(service, attempt),
	}

	// A floating IP is only allocated when the request carries the fip field,
	// even an empty one, which stands for "pick any address". checkService
	// makes sure an external load balancer has a floating network.
	if !svcConf.internal {
		createReq.Fip = ptr.To(svcConf.requestedFloatingIP)
		createReq.FipNetworkId = ptr.To(svcConf.lbPublicNetworkID)
	}

	mc := metrics.NewMetricContext("loadbalancer", "create")
	resp, err := lbaas.lb.CreateLoadBalancer(ctx, createReq)
	if mc.ObserveRequest(err) != nil {
		// A load balancer that was deleted with this idempotency key but is
		// not purged yet makes the service fail the create. Move on to the
		// next key so that the next reconcile does not hit it again.
		if status.Code(err) == codes.Internal {
			lbaas.updateServiceAnnotation(service, ServiceAnnotationLoadBalancerCreateAttempt, strconv.Itoa(attempt+1))
		}
		return nil, err
	}

	// The service answers a create carrying a known idempotency key with the
	// load balancer made for that key, also when that one is on its way out.
	if resp.GetState() == lbv1.State_STATE_DELETING || resp.GetState() == lbv1.State_STATE_DELETED {
		lbaas.updateServiceAnnotation(service, ServiceAnnotationLoadBalancerCreateAttempt, strconv.Itoa(attempt+1))
		return nil, fmt.Errorf("the previous load balancer %s is still being deleted", resp.GetId())
	}

	// Make sure the LB ID is saved even when the load balancer never becomes ready.
	lbaas.updateServiceAnnotation(service, ServiceAnnotationLoadBalancerID, resp.GetId())
	lbaas.updateServiceAnnotation(service, ServiceAnnotationLoadBalancerCreateAttempt, strconv.Itoa(attempt))
	lbaas.updateServiceAnnotation(service, ServiceAnnotationLoadBalancerConfigHash, configHash(spec))

	return lbaas.waitLoadBalancerReady(ctx, resp.GetId())
}

// updateLoadBalancerConfig brings the configuration of an existing load
// balancer in line with spec. Every update makes the service provision the
// load balancer again, even an update that changes nothing, so it is only
// sent when the hash of the configuration differs from the one recorded on
// the Service. When wait is set, the load balancer is returned once it is
// ready again.
func (lbaas *LbaasV2) updateLoadBalancerConfig(ctx context.Context, service *corev1.Service, loadbalancer *lbv1.LoadBalancer, spec *lbv1.LoadBalancerSpec, wait bool) (*lbv1.LoadBalancer, error) {
	hash := configHash(spec)
	if getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerConfigHash, "") == hash {
		return loadbalancer, nil
	}

	// An update sent while the load balancer is still being provisioned
	// strands it: it ends up failed and deleted. A failed one is updated, the
	// update is its retry.
	switch loadbalancer.GetState() {
	case lbv1.State_STATE_READY, lbv1.State_STATE_FAILED:
	default:
		return nil, fmt.Errorf("load balancer %s is not READY, current state: %s; its configuration is updated once it is", loadbalancer.GetId(), loadBalancerStateDetail(loadbalancer))
	}

	current := loadbalancer.GetSpec()
	klog.InfoS("Updating load balancer", "lbID", loadbalancer.GetId(), "service", klog.KObj(service))
	mc := metrics.NewMetricContext("loadbalancer", "update")
	_, err := lbaas.lb.UpdateLoadBalancer(ctx, &lbv1.UpdateLoadBalancerRequest{
		Id:                    loadbalancer.GetId(),
		Listeners:             mergeListenerIDs(spec.GetListeners(), current.GetListeners()),
		ClientConnLimit:       ptr.To(spec.GetClientConnLimit()),
		SecurityGroupDisabled: ptr.To(spec.GetSecurityGroupDisabled()),
		SecurityGroupIds:      spec.GetSecurityGroupIds(),
		QosPolicyDisabled:     ptr.To(spec.GetQosPolicyDisabled()),
		QosPolicyId:           ptr.To(spec.GetQosPolicyId()),
		VpcCidr:               ptr.To(spec.GetVpcCidr()),
	})
	if mc.ObserveRequest(err) != nil {
		return nil, fmt.Errorf("failed to update load balancer %s: %v", loadbalancer.GetId(), err)
	}

	// Recorded only once the service accepted the update, so that a failed
	// one is sent again by the next reconcile.
	lbaas.updateServiceAnnotation(service, ServiceAnnotationLoadBalancerConfigHash, hash)

	if !wait {
		return loadbalancer, nil
	}
	return lbaas.waitLoadBalancerReady(ctx, loadbalancer.GetId())
}

// checkNetworkUnchanged reports a Service whose subnet or network no longer
// matches its load balancer's: neither can be changed after creation.
func (lbaas *LbaasV2) checkNetworkUnchanged(service *corev1.Service, loadbalancer *lbv1.LoadBalancer, spec *lbv1.LoadBalancerSpec) {
	current := loadbalancer.GetSpec()
	if spec.GetSubnetId() != "" && current.GetSubnetId() != "" && !sameID(spec.GetSubnetId(), current.GetSubnetId()) ||
		spec.GetNetworkId() != "" && current.GetNetworkId() != "" && !sameID(spec.GetNetworkId(), current.GetNetworkId()) {
		msg := "Load balancer %s of Service %s/%s stays on network %s subnet %s, the network and subnet of a load balancer cannot be changed; delete and recreate the Service to move it"
		lbaas.eventRecorder.Eventf(service, corev1.EventTypeWarning, eventLBNetworkChangeIgnored, msg, loadbalancer.GetId(), service.Namespace, service.Name, current.GetNetworkId(), current.GetSubnetId())
		klog.Warningf(msg, loadbalancer.GetId(), service.Namespace, service.Name, current.GetNetworkId(), current.GetSubnetId())
	}
}

// sameID compares two OpenStack UUIDs, which the service accepts with or without dashes.
func sameID(a, b string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "-", "")) }
	return norm(a) == norm(b)
}

// GetLoadBalancer returns whether the specified load balancer exists and its status
func (lbaas *LbaasV2) GetLoadBalancer(ctx context.Context, clusterName string, service *corev1.Service) (*corev1.LoadBalancerStatus, bool, error) {
	name := lbaas.GetLoadBalancerName(ctx, clusterName, service)
	lbID := getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerID, "")
	var loadbalancer *lbv1.LoadBalancer
	var err error

	if lbID != "" {
		loadbalancer, err = getLoadbalancerByID(ctx, lbaas.lb, lbID)
	} else {
		loadbalancer, err = getLoadbalancerByName(ctx, lbaas.lb, lbaas.tenantID, name)
	}
	if err != nil && cpoerrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if isLoadBalancerGone(loadbalancer) {
		return nil, false, nil
	}

	addr := loadBalancerAddress(loadbalancer)
	if addr == "" {
		return &corev1.LoadBalancerStatus{}, true, nil
	}
	return lbaas.createLoadBalancerStatus(service, addr), true, nil
}

// loadBalancerAddress returns the address clients reach the load balancer at:
// its floating IP when it has one, its VIP otherwise.
func loadBalancerAddress(lb *lbv1.LoadBalancer) string {
	if lb.GetFipEnabled() && lb.GetFip() != "" {
		return lb.GetFip()
	}
	return lb.GetIp()
}

// GetLoadBalancerName returns the constructed load balancer name.
func (lbaas *LbaasV2) GetLoadBalancerName(_ context.Context, clusterName string, service *corev1.Service) string {
	return cpoutil.Sprintf255(lbFormat, servicePrefix, clusterName, service.Namespace, service.Name)
}

// The LB needs to be configured with instance addresses on the same
// subnet as the LB (aka opts.SubnetID). Currently, we're just
// guessing that the node's InternalIP is the right address.
// In case no InternalIP can be found, ExternalIP is tried.
// If neither InternalIP nor ExternalIP can be found an error is
// returned.
// If preferredIPFamily is specified, only address of the specified IP family can be returned.
func nodeAddressForLB(node *corev1.Node, preferredIPFamily corev1.IPFamily) (string, error) {
	addrs := node.Status.Addresses
	if len(addrs) == 0 {
		return "", cpoerrors.ErrNoAddressFound
	}

	allowedAddrTypes := []corev1.NodeAddressType{corev1.NodeInternalIP, corev1.NodeExternalIP}
	for _, allowedAddrType := range allowedAddrTypes {
		for _, addr := range addrs {
			if addr.Type == allowedAddrType {
				switch preferredIPFamily {
				case corev1.IPv4Protocol:
					if netutils.IsIPv4String(addr.Address) {
						return addr.Address, nil
					}
				case corev1.IPv6Protocol:
					if netutils.IsIPv6String(addr.Address) {
						return addr.Address, nil
					}
				default:
					return addr.Address, nil
				}
			}
		}
	}

	return "", cpoerrors.ErrNoAddressFound
}

// getKeyValueFromServiceAnnotation converts a comma-separated list of key-value
// pairs from the specified annotation into a map or returns the specified
// defaultSetting if the annotation is empty
func getKeyValueFromServiceAnnotation(service *corev1.Service, annotationKey string, defaultSetting string) map[string]string {
	annotationValue := getStringFromServiceAnnotation(service, annotationKey, defaultSetting)
	return cpoutil.StringToMap(annotationValue)
}

// getStringFromServiceAnnotation searches a given v1.Service for a specific annotationKey and either returns the annotation's value or a specified defaultSetting
func getStringFromServiceAnnotation(service *corev1.Service, annotationKey string, defaultSetting string) string {
	klog.V(4).Infof("getStringFromServiceAnnotation(%s/%s, %v, %v)", service.Namespace, service.Name, annotationKey, defaultSetting)
	if annotationValue, ok := service.Annotations[annotationKey]; ok {
		//if there is an annotation for this setting, set the "setting" var to it
		// annotationValue can be empty, it is working as designed
		// it makes possible for instance provisioning loadbalancer without floatingip
		klog.V(4).Infof("Found a Service Annotation: %v = %v", annotationKey, annotationValue)
		return annotationValue
	}
	//if there is no annotation, set "settings" var to the value from cloud config
	if defaultSetting != "" {
		klog.V(4).Infof("Could not find a Service Annotation; falling back on cloud-config setting: %v = %v", annotationKey, defaultSetting)
	}
	return defaultSetting
}

// getIntFromServiceAnnotation searches a given v1.Service for a specific annotationKey and either returns the annotation's integer value or a specified defaultSetting
func getIntFromServiceAnnotation(service *corev1.Service, annotationKey string, defaultSetting int) int {
	klog.V(4).Infof("getIntFromServiceAnnotation(%s/%s, %v, %v)", service.Namespace, service.Name, annotationKey, defaultSetting)
	if annotationValue, ok := service.Annotations[annotationKey]; ok {
		returnValue, err := strconv.Atoi(annotationValue)
		if err != nil {
			klog.Warningf("Could not parse int value from %q, failing back to default %s = %v, %v", annotationValue, annotationKey, defaultSetting, err)
			return defaultSetting
		}

		klog.V(4).Infof("Found a Service Annotation: %v = %v", annotationKey, annotationValue)
		return returnValue
	}
	klog.V(4).Infof("Could not find a Service Annotation; falling back to default setting: %v = %v", annotationKey, defaultSetting)
	return defaultSetting
}

// getBoolFromServiceAnnotation searches a given v1.Service for a specific annotationKey and either returns the annotation's boolean value or a specified defaultSetting
// If the annotation is not found or is not a valid boolean ("true" or "false"), it falls back to the defaultSetting and logs a message accordingly.
func getBoolFromServiceAnnotation(service *corev1.Service, annotationKey string, defaultSetting bool) bool {
	klog.V(4).Infof("getBoolFromServiceAnnotation(%s/%s, %v, %v)", service.Namespace, service.Name, annotationKey, defaultSetting)
	if annotationValue, ok := service.Annotations[annotationKey]; ok {
		returnValue := false
		switch annotationValue {
		case "true":
			returnValue = true
		case "false":
			returnValue = false
		default:
			klog.Infof("Found a non-boolean Service Annotation: %v = %v (falling back to default setting: %v)", annotationKey, annotationValue, defaultSetting)
			return defaultSetting
		}

		klog.V(4).Infof("Found a Service Annotation: %v = %v", annotationKey, returnValue)
		return returnValue
	}
	klog.V(4).Infof("Could not find a Service Annotation; falling back to default setting: %v = %v", annotationKey, defaultSetting)
	return defaultSetting
}

// getSubnetIDForLB returns subnet-id for a specific node
func getSubnetIDForLB(ctx context.Context, network *gophercloud.ServiceClient, node corev1.Node, preferredIPFamily corev1.IPFamily) (string, error) {
	ipAddress, err := nodeAddressForLB(&node, preferredIPFamily)
	if err != nil {
		return "", err
	}

	instanceID, _, err := instanceIDFromProviderID(node.Spec.ProviderID)
	if err != nil {
		return "", fmt.Errorf("can't determine instance ID from ProviderID when autodetecting LB subnet: %w", err)
	}

	ports, err := getAttachedPorts(ctx, network, instanceID)
	if err != nil {
		return "", err
	}

	for _, port := range ports {
		for _, fixedIP := range port.FixedIPs {
			if fixedIP.IPAddress == ipAddress {
				return fixedIP.SubnetID, nil
			}
		}
	}

	return "", cpoerrors.ErrNotFound
}

// isPortMember returns true if IP and subnetID are one of the FixedIPs on the port
func isPortMember(port PortWithPortSecurity, ip string, subnetID string) bool {
	for _, fixedIP := range port.FixedIPs {
		if (subnetID == "" || subnetID == fixedIP.SubnetID) && ip == fixedIP.IPAddress {
			return true
		}
	}
	return false
}

// ensureFloatingIP makes the load balancer's floating IP match what the
// Service asks for and returns the address the Service is reached at.
func (lbaas *LbaasV2) ensureFloatingIP(ctx context.Context, service *corev1.Service, lb *lbv1.LoadBalancer, svcConf *serviceConfig) (string, error) {
	serviceName := fmt.Sprintf("%s/%s", service.Namespace, service.Name)

	// We need to fetch the FIP attached to load balancer's VIP port for both codepaths
	if svcConf.internal {
		// if we found a FIP, this is an internal service and we are the owner we should remove it
		if lb.GetFipEnabled() {
			klog.InfoS("Releasing the floating IP of the internal Service's load balancer", "lbID", lb.GetId(), "floatingIP", lb.GetFip(), "service", klog.KObj(service))
			lbaas.eventRecorder.Eventf(service, corev1.EventTypeWarning, eventLBForceInternal, "Releasing floating IP %s of load balancer %s because Service %s is internal", lb.GetFip(), lb.GetId(), serviceName)
			mc := metrics.NewMetricContext("floating_ip", "release")
			_, err := lbaas.lb.ReleaseFloatingIp(ctx, &lbv1.ReleaseFloatingIpRequest{Id: lb.GetId()})
			if mc.ObserveRequest(err) != nil {
				return "", fmt.Errorf("failed to release the floating IP of load balancer %s: %v", lb.GetId(), err)
			}
		}
		return lb.GetIp(), nil
	}

	// first attempt: if we've found a FIP attached to LBs VIP port, we'll be using that.
	if lb.GetFipEnabled() {
		if lb.GetFipState() != lbv1.FipState_FIP_STATE_ACTIVE || lb.GetFip() == "" {
			return "", fmt.Errorf("floating IP of load balancer %s is not active yet, current state: %s", lb.GetId(), lb.GetFipState())
		}
		return lb.GetFip(), nil
	}

	// we cannot add a FIP to this LB without knowing the external network
	if svcConf.lbPublicNetworkID == "" {
		return "", fmt.Errorf("load balancer %s of service %s needs a floating IP but no floating network is configured", lb.GetId(), serviceName)
	}

	klog.InfoS("Allocating a floating IP for the load balancer", "lbID", lb.GetId(), "floatingNetwork", svcConf.lbPublicNetworkID, "service", klog.KObj(service))
	mc := metrics.NewMetricContext("floating_ip", "allocate")
	_, err := lbaas.lb.AllocateFloatingIp(ctx, &lbv1.AllocateFloatingIpRequest{
		Id:           lb.GetId(),
		Fip:          svcConf.requestedFloatingIP,
		FipNetworkId: svcConf.lbPublicNetworkID,
	})
	if mc.ObserveRequest(err) != nil {
		return "", fmt.Errorf("failed to allocate a floating IP for load balancer %s: %v", lb.GetId(), err)
	}

	return "", fmt.Errorf("floating IP of load balancer %s is being allocated", lb.GetId())
}

// checkServicePorts validates the ports of the Service against what the load balancer service accepts.
func checkServicePorts(service *corev1.Service) error {
	ports := service.Spec.Ports
	if len(ports) == 0 {
		return fmt.Errorf("no service ports provided")
	}

	// The load balancer service identifies a listener by its port number
	// alone, so the same number cannot be used for both TCP and UDP.
	seen := make(map[int32]corev1.Protocol, len(ports))
	for _, port := range ports {
		if _, err := listenerProtocol(port); err != nil {
			return err
		}
		if protocol, ok := seen[port.Port]; ok {
			return fmt.Errorf("port %d is used by more than one Service port (%s and %s), a load balancer can listen on a port number only once", port.Port, protocol, port.Protocol)
		}
		seen[port.Port] = port.Protocol
	}

	return nil
}

func (lbaas *LbaasV2) checkServiceUpdate(service *corev1.Service, svcConf *serviceConfig) error {
	if err := checkServicePorts(service); err != nil {
		return err
	}
	serviceName := fmt.Sprintf("%s/%s", service.Namespace, service.Name)

	if len(service.Spec.IPFamilies) > 0 {
		// Since OCCM does not support multiple load-balancers per service yet,
		// the first IP family will determine the IP family of the load-balancer
		svcConf.preferredIPFamily = service.Spec.IPFamilies[0]
	}

	return lbaas.makeSvcConf(serviceName, service, svcConf)
}

func (lbaas *LbaasV2) checkService(ctx context.Context, service *corev1.Service, nodes []*corev1.Node, svcConf *serviceConfig) error {
	serviceName := fmt.Sprintf("%s/%s", service.Namespace, service.Name)

	if len(nodes) == 0 {
		return fmt.Errorf("there are no available nodes for LoadBalancer service %s", serviceName)
	}
	if err := checkServicePorts(service); err != nil {
		return err
	}

	if len(service.Spec.IPFamilies) > 0 {
		// Since OCCM does not support multiple load-balancers per service yet,
		// the first IP family will determine the IP family of the load-balancer
		svcConf.preferredIPFamily = service.Spec.IPFamilies[0]
	}
	if svcConf.preferredIPFamily == corev1.IPv6Protocol {
		return fmt.Errorf("IPv6 load balancers are not supported, Service %s must use IPv4 as its first IP family", serviceName)
	}

	// The deployment mode decides for every Service: with internal-lb=true no
	// load balancer gets a floating IP, otherwise every load balancer gets one.
	// A Service cannot choose differently.
	svcConf.internal = lbaas.opts.InternalLB
	if _, ok := service.Annotations[ServiceAnnotationLoadBalancerInternal]; ok {
		if getBoolFromServiceAnnotation(service, ServiceAnnotationLoadBalancerInternal, svcConf.internal) != svcConf.internal {
			mode := "external"
			if svcConf.internal {
				mode = "internal"
			}
			msg := "Annotation %s of Service %s is ignored, the load balancers of this cluster are %s"
			lbaas.eventRecorder.Eventf(service, corev1.EventTypeWarning, eventLBInternalAnnotationIgnored, msg, ServiceAnnotationLoadBalancerInternal, serviceName, mode)
			klog.Warningf(msg, ServiceAnnotationLoadBalancerInternal, serviceName, mode)
		}
	}

	// Reported here only: UpdateLoadBalancer runs for every Service on every
	// node change and would repeat these events each time.
	var ignored []string
	for _, annotation := range unsupportedServiceAnnotations {
		if _, ok := service.Annotations[annotation]; ok {
			ignored = append(ignored, annotation)
		}
	}
	if len(ignored) > 0 {
		msg := "Annotations %s of Service %s are ignored because the load balancer service does not support them"
		lbaas.eventRecorder.Eventf(service, corev1.EventTypeWarning, eventLBAnnotationIgnored, msg, strings.Join(ignored, ", "), serviceName)
		klog.Warningf(msg, strings.Join(ignored, ", "), serviceName)
	}
	if len(service.Spec.LoadBalancerSourceRanges) > 0 {
		msg := "LoadBalancerSourceRanges are ignored for Service %s because the load balancer service does not support it"
		lbaas.eventRecorder.Eventf(service, corev1.EventTypeWarning, eventLBSourceRangesIgnored, msg, serviceName)
		klog.Warningf(msg, serviceName)
	}

	svcConf.lbNetworkID = getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerNetworkID, lbaas.opts.NetworkID)
	svcConf.lbSubnetID = getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerSubnetID, lbaas.opts.SubnetID)

	if len(svcConf.lbSubnetID) == 0 {
		subnetID, err := getSubnetIDForLB(ctx, lbaas.network, *nodes[0], svcConf.preferredIPFamily)
		if err != nil {
			return fmt.Errorf("failed to get subnet to create load balancer for service %s: %v", serviceName, err)
		}
		svcConf.lbSubnetID = subnetID
	}
	// The load balancer service wants the network of the subnet spelled out.
	if len(svcConf.lbNetworkID) == 0 {
		mc := metrics.NewMetricContext("subnet", "get")
		subnet, err := subnets.Get(ctx, lbaas.network, svcConf.lbSubnetID).Extract()
		if mc.ObserveRequest(err) != nil {
			return fmt.Errorf("failed to find the network of subnet %q to create load balancer for service %s: %v", svcConf.lbSubnetID, serviceName, err)
		}
		svcConf.lbNetworkID = subnet.NetworkID
	}
	// The load balancer reaches the nodes from its own subnet.
	svcConf.lbMemberSubnetID = svcConf.lbSubnetID

	if !svcConf.internal {
		klog.V(4).Infof("Ensure an external loadbalancer service")

		// External load balancers always get a floating IP, so the network to
		// allocate it from is required.
		floatingNetworkID := getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerFloatingNetworkID, lbaas.opts.FloatingNetworkID)
		if floatingNetworkID == "" {
			return fmt.Errorf("no floating network for the external load balancer of service %s: set floating-network-id in the [LoadBalancer] section of the cloud config", serviceName)
		}

		svcConf.lbPublicNetworkID = floatingNetworkID
		svcConf.requestedFloatingIP = service.Spec.LoadBalancerIP
	} else {
		klog.V(4).Infof("Ensure an internal loadbalancer service.")
	}
	return lbaas.makeSvcConf(serviceName, service, svcConf)
}

func (lbaas *LbaasV2) makeSvcConf(serviceName string, service *corev1.Service, svcConf *serviceConfig) error {
	svcConf.tenantID = lbaas.tenantID
	svcConf.lbID = getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerID, "")

	// The load balancer service uses 0 for "no limit", Octavia used -1.
	svcConf.connLimit = max(getIntFromServiceAnnotation(service, ServiceAnnotationLoadBalancerConnLimit, 0), 0)

	algorithm, err := parseAlgorithm(getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerLbMethod, lbaas.opts.LBMethod))
	if err != nil {
		return fmt.Errorf("invalid load balancing method for service %s: %v", serviceName, err)
	}
	svcConf.algorithm = algorithm

	// Get service node-selector annotations
	svcConf.nodeSelectors = getKeyValueFromServiceAnnotation(service, ServiceAnnotationLoadBalancerNodeSelector, lbaas.opts.NodeSelector)
	for key, value := range svcConf.nodeSelectors {
		if value == "" {
			klog.V(3).Infof("Target node label %s key is set to LoadBalancer service %s", key, serviceName)
		} else {
			klog.V(3).Infof("Target node label %s=%s is set to LoadBalancer service %s", key, value, serviceName)
		}
	}

	http, err := getHTTPConfig(service)
	if err != nil {
		return fmt.Errorf("invalid HTTP configuration for service %s: %v", serviceName, err)
	}
	svcConf.http = http

	svcConf.enableMonitor = getBoolFromServiceAnnotation(service, ServiceAnnotationLoadBalancerEnableHealthMonitor, lbaas.opts.CreateMonitor)
	svcConf.healthMonitorDelay = getIntFromServiceAnnotation(service, ServiceAnnotationLoadBalancerHealthMonitorDelay, int(lbaas.opts.MonitorDelay.Seconds()))
	svcConf.healthMonitorTimeout = getIntFromServiceAnnotation(service, ServiceAnnotationLoadBalancerHealthMonitorTimeout, int(lbaas.opts.MonitorTimeout.Seconds()))
	svcConf.healthMonitorMaxRetries = getIntFromServiceAnnotation(service, ServiceAnnotationLoadBalancerHealthMonitorMaxRetries, int(lbaas.opts.MonitorMaxRetries))
	svcConf.healthMonitorMaxRetriesDown = getIntFromServiceAnnotation(service, ServiceAnnotationLoadBalancerHealthMonitorMaxRetriesDown, int(lbaas.opts.MonitorMaxRetriesDown))
	if svcConf.enableMonitor {
		// Checked here so that the reason reaches the Service as an event
		// instead of arriving as a generic InvalidArgument from the service.
		if svcConf.healthMonitorDelay < 1 || svcConf.healthMonitorTimeout < 1 {
			return fmt.Errorf("invalid health monitor for service %s: delay (%ds) and timeout (%ds) must be at least 1 second", serviceName, svcConf.healthMonitorDelay, svcConf.healthMonitorTimeout)
		}
		if svcConf.healthMonitorTimeout >= svcConf.healthMonitorDelay {
			return fmt.Errorf("invalid health monitor for service %s: timeout (%ds) must be less than delay (%ds)", serviceName, svcConf.healthMonitorTimeout, svcConf.healthMonitorDelay)
		}
		if svcConf.healthMonitorMaxRetries < 1 || svcConf.healthMonitorMaxRetriesDown < 1 {
			return fmt.Errorf("invalid health monitor for service %s: max-retries (%d) and max-retries-down (%d) must be at least 1", serviceName, svcConf.healthMonitorMaxRetries, svcConf.healthMonitorMaxRetriesDown)
		}
	}
	return nil
}

func (lbaas *LbaasV2) updateServiceAnnotation(service *corev1.Service, key, value string) {
	if service.Annotations == nil {
		service.Annotations = map[string]string{}
	}
	service.Annotations[key] = value
}

// createLoadBalancerStatus creates the loadbalancer status from the different possible sources
func (lbaas *LbaasV2) createLoadBalancerStatus(service *corev1.Service, addr string) *corev1.LoadBalancerStatus {
	status := &corev1.LoadBalancerStatus{}
	// If hostname is explicetly set
	if hostname := getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerLoadbalancerHostname, ""); hostname != "" {
		status.Ingress = []corev1.LoadBalancerIngress{{Hostname: hostname}}
		return status
	}

	if lbaas.opts.EnableIngressHostname {
		fakeHostname := fmt.Sprintf("%s.%s", addr, lbaas.opts.IngressHostnameSuffix)
		status.Ingress = []corev1.LoadBalancerIngress{{Hostname: fakeHostname}}
		return status
	}

	// Default to IP
	ipMode := corev1.LoadBalancerIPModeVIP
	status.Ingress = []corev1.LoadBalancerIngress{{
		IP:     addr,
		IPMode: &ipMode,
	}}
	return status
}

func (lbaas *LbaasV2) ensureLoadBalancer(ctx context.Context, clusterName string, service *corev1.Service, nodes []*corev1.Node) (lbs *corev1.LoadBalancerStatus, err error) {
	svcConf := new(serviceConfig)

	// Update the service annotations(e.g. add loadbalancer.openstack.org/load-balancer-id) in the end if it doesn't exist.
	patcher := newServicePatcher(lbaas.kclient, service)
	defer func() { err = patcher.Patch(ctx, err) }()

	if err := lbaas.checkService(ctx, service, nodes, svcConf); err != nil {
		return nil, err
	}

	// apply node-selector to a list of nodes
	filteredNodes := filterNodes(nodes, svcConf.nodeSelectors)

	spec, err := lbaas.buildLoadBalancerSpec(service, filteredNodes, svcConf)
	if err != nil {
		return nil, err
	}

	lbName := lbaas.GetLoadBalancerName(ctx, clusterName, service)
	serviceName := fmt.Sprintf("%s/%s", service.Namespace, service.Name)
	var loadbalancer *lbv1.LoadBalancer
	createNewLB := false

	// Check the load balancer in the Service annotation.
	if svcConf.lbID != "" {
		loadbalancer, err = getLoadbalancerByID(ctx, lbaas.lb, svcConf.lbID)
		if err != nil && !cpoerrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get load balancer %s: %v", svcConf.lbID, err)
		}
		if isLoadBalancerGone(loadbalancer) {
			// The load balancer was deleted behind our back. Its idempotency
			// key must not be used again, the service would answer with the
			// deleted load balancer.
			// The ID is dropped so that this happens once, not on every
			// reconcile until a new load balancer exists.
			klog.InfoS("Load balancer in the Service annotation does not exist anymore", "lbID", svcConf.lbID, "service", klog.KObj(service))
			lbaas.updateServiceAnnotation(service, ServiceAnnotationLoadBalancerCreateAttempt, strconv.Itoa(getCreateAttempt(service)+1))
			delete(service.Annotations, ServiceAnnotationLoadBalancerID)
			delete(service.Annotations, ServiceAnnotationLoadBalancerConfigHash)
			loadbalancer = nil
		} else if loadbalancer.GetName() != lbName {
			// The annotation can be set by anyone who can edit the Service.
			// Attaching to a load balancer made for another Service would
			// hand out its address, and sharing is not supported.
			return nil, fmt.Errorf("load balancer %s (%s) in annotation %s was not created for Service %s, sharing a load balancer is not supported",
				loadbalancer.GetId(), loadbalancer.GetName(), ServiceAnnotationLoadBalancerID, serviceName)
		}
	}

	if loadbalancer == nil {
		loadbalancer, err = getLoadbalancerByName(ctx, lbaas.lb, svcConf.tenantID, lbName)
		if err != nil {
			if err != cpoerrors.ErrNotFound {
				return nil, fmt.Errorf("error getting loadbalancer for Service %s: %v", serviceName, err)
			}
			klog.InfoS("Creating loadbalancer", "lbName", lbName, "service", klog.KObj(service))
			loadbalancer, err = lbaas.createLoadBalancer(ctx, lbName, service, spec, svcConf)
			if err != nil {
				return nil, fmt.Errorf("error creating loadbalancer %s: %v", lbName, err)
			}
			createNewLB = true
		}
	}

	// Make sure LB ID will be saved at this point.
	lbaas.updateServiceAnnotation(service, ServiceAnnotationLoadBalancerID, loadbalancer.GetId())

	// This is an existing load balancer, bring its configuration in line with the Service and the nodes.
	if !createNewLB {
		lbaas.checkNetworkUnchanged(service, loadbalancer, spec)
		loadbalancer, err = lbaas.updateLoadBalancerConfig(ctx, service, loadbalancer, spec, true)
		if err != nil {
			return nil, err
		}
	}

	if loadbalancer.GetState() != lbv1.State_STATE_READY {
		return nil, fmt.Errorf("load balancer %s is not READY, current state: %s", loadbalancer.GetId(), loadBalancerStateDetail(loadbalancer))
	}

	klog.V(4).InfoS("Load balancer ensured", "lbID", loadbalancer.GetId(), "createNewLB", createNewLB)

	addr, err := lbaas.ensureFloatingIP(ctx, service, loadbalancer, svcConf)
	if err != nil {
		return nil, err
	}
	if addr == "" {
		return nil, fmt.Errorf("load balancer %s has no address yet", loadbalancer.GetId())
	}

	// save address into the annotation
	lbaas.updateServiceAnnotation(service, ServiceAnnotationLoadBalancerAddress, addr)

	// Create status the load balancer
	status := lbaas.createLoadBalancerStatus(service, addr)

	if lbaas.opts.ManageSecurityGroups {
		err := lbaas.ensureAndUpdateOctaviaSecurityGroup(ctx, clusterName, service, filteredNodes, svcConf)
		if err != nil {
			return status, fmt.Errorf("failed when reconciling security groups for LB service %v/%v: %v", service.Namespace, service.Name, err)
		}
	} else {
		// Attempt to delete the SG if `manage-security-groups` is disabled. When CPO is reconfigured to enable it we
		// will reconcile the LB and create the SG. This is to make sure it works the same in the opposite direction.
		if err := lbaas.ensureSecurityGroupDeleted(ctx, service); err != nil {
			return status, err
		}
	}

	return status, nil
}

// EnsureLoadBalancer creates a new load balancer or updates the existing one.
func (lbaas *LbaasV2) EnsureLoadBalancer(ctx context.Context, clusterName string, apiService *corev1.Service, nodes []*corev1.Node) (*corev1.LoadBalancerStatus, error) {
	mc := metrics.NewMetricContext("loadbalancer", "ensure")
	klog.InfoS("EnsureLoadBalancer", "cluster", clusterName, "service", klog.KObj(apiService))
	status, err := lbaas.ensureLoadBalancer(ctx, clusterName, apiService, nodes)
	return status, mc.ObserveReconcile(err)
}

func (lbaas *LbaasV2) updateLoadBalancer(ctx context.Context, clusterName string, service *corev1.Service, nodes []*corev1.Node) (err error) {
	svcConf := new(serviceConfig)
	if err := lbaas.checkServiceUpdate(service, svcConf); err != nil {
		return err
	}

	// Save the new configuration hash on the Service in the end.
	patcher := newServicePatcher(lbaas.kclient, service)
	defer func() { err = patcher.Patch(ctx, err) }()

	// apply node-selector to a list of nodes
	filteredNodes := filterNodes(nodes, svcConf.nodeSelectors)

	serviceName := fmt.Sprintf("%s/%s", service.Namespace, service.Name)
	klog.V(2).Infof("Updating %d nodes for Service %s in cluster %s", len(filteredNodes), serviceName, clusterName)

	spec, err := lbaas.buildLoadBalancerSpec(service, filteredNodes, svcConf)
	if err != nil {
		return err
	}

	// The service controller calls this for every LoadBalancer Service on
	// every node change; for most of them nothing changes.
	if getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerConfigHash, "") == configHash(spec) {
		return nil
	}

	// Get load balancer
	lbName := lbaas.GetLoadBalancerName(ctx, clusterName, service)
	var loadbalancer *lbv1.LoadBalancer
	if svcConf.lbID != "" {
		loadbalancer, err = getLoadbalancerByID(ctx, lbaas.lb, svcConf.lbID)
	} else {
		loadbalancer, err = getLoadbalancerByName(ctx, lbaas.lb, svcConf.tenantID, lbName)
	}
	if err != nil {
		return fmt.Errorf("failed to get the load balancer of service %s: %v", serviceName, err)
	}
	if isLoadBalancerGone(loadbalancer) {
		return fmt.Errorf("load balancer %s of service %s does not exist anymore", loadbalancer.GetId(), serviceName)
	}
	if loadbalancer.GetName() != lbName {
		return fmt.Errorf("load balancer %s (%s) in annotation %s was not created for Service %s", loadbalancer.GetId(), loadbalancer.GetName(), ServiceAnnotationLoadBalancerID, serviceName)
	}

	// Node events come in bursts while a cluster scales; the next
	// EnsureLoadBalancer reports whether the load balancer became ready.
	if _, err := lbaas.updateLoadBalancerConfig(ctx, service, loadbalancer, spec, false); err != nil {
		return err
	}

	if lbaas.opts.ManageSecurityGroups {
		svcConf.lbMemberSubnetID = loadbalancer.GetSpec().GetSubnetId()
		if err := lbaas.ensureAndUpdateOctaviaSecurityGroup(ctx, clusterName, service, filteredNodes, svcConf); err != nil {
			return fmt.Errorf("failed to update Security Group for loadbalancer service %s: %v", serviceName, err)
		}
	}
	// We don't try to lookup and delete the SG here when `manage-security-group=false` as `UpdateLoadBalancer()` is
	// only called on changes to the list of the Nodes. Deletion of the SG on reconfiguration will be handled by
	// EnsureLoadBalancer() that is the true LB reconcile function.

	return nil
}

// UpdateLoadBalancer updates hosts under the specified load balancer.
func (lbaas *LbaasV2) UpdateLoadBalancer(ctx context.Context, clusterName string, service *corev1.Service, nodes []*corev1.Node) error {
	mc := metrics.NewMetricContext("loadbalancer", "update")
	err := lbaas.updateLoadBalancer(ctx, clusterName, service, nodes)
	return mc.ObserveReconcile(err)
}

// EnsureLoadBalancerDeleted deletes the specified load balancer
func (lbaas *LbaasV2) EnsureLoadBalancerDeleted(ctx context.Context, clusterName string, service *corev1.Service) error {
	mc := metrics.NewMetricContext("loadbalancer", "delete")
	err := lbaas.ensureLoadBalancerDeleted(ctx, clusterName, service)
	return mc.ObserveReconcile(err)
}

// deleteLoadBalancer asks the service to delete the load balancer and waits until it is gone.
func (lbaas *LbaasV2) deleteLoadBalancer(ctx context.Context, loadbalancer *lbv1.LoadBalancer, service *corev1.Service) error {
	if loadbalancer.GetState() != lbv1.State_STATE_DELETING && loadbalancer.GetState() != lbv1.State_STATE_DELETED {
		keepFloatingIP := getBoolFromServiceAnnotation(service, ServiceAnnotationLoadBalancerKeepFloatingIP, false)
		klog.InfoS("Deleting load balancer", "lbID", loadbalancer.GetId(), "service", klog.KObj(service), "keepFloatingIP", keepFloatingIP)

		mc := metrics.NewMetricContext("loadbalancer", "delete")
		_, err := lbaas.lb.DeleteLoadBalancer(ctx, &lbv1.DeleteLoadBalancerRequest{
			Id:                 loadbalancer.GetId(),
			PreserveFloatingIp: keepFloatingIP,
		})
		if lbapi.IsNotFound(err) {
			_ = mc.ObserveRequest(nil)
			return nil
		}
		if mc.ObserveRequest(err) != nil {
			return fmt.Errorf("failed to delete loadbalancer %s: %v", loadbalancer.GetId(), err)
		}
	}

	if err := lbaas.waitLoadBalancerDeleted(ctx, loadbalancer.GetId()); err != nil {
		return err
	}
	klog.InfoS("Deleted load balancer", "lbID", loadbalancer.GetId(), "service", klog.KObj(service))

	return nil
}

func (lbaas *LbaasV2) ensureLoadBalancerDeleted(ctx context.Context, clusterName string, service *corev1.Service) error {
	lbName := lbaas.GetLoadBalancerName(ctx, clusterName, service)
	var loadbalancer *lbv1.LoadBalancer
	var err error

	if lbID := getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerID, ""); lbID != "" {
		loadbalancer, err = getLoadbalancerByID(ctx, lbaas.lb, lbID)
		if err != nil && !cpoerrors.IsNotFound(err) {
			return err
		}
		// Only delete what was created for this Service: the ID in the
		// annotation can be changed by anyone who can edit the Service.
		if loadbalancer != nil && loadbalancer.GetName() != lbName {
			klog.Warningf("Not deleting load balancer %s (%s) referenced by Service %s/%s: it was not created for this Service", loadbalancer.GetId(), loadbalancer.GetName(), service.Namespace, service.Name)
			loadbalancer = nil
		}
	}
	// Without a usable ID the Service's load balancer can still exist, for
	// instance when the creation response was lost or the annotation was edited.
	if loadbalancer == nil {
		loadbalancer, err = getLoadbalancerByName(ctx, lbaas.lb, lbaas.tenantID, lbName)
		if err != nil && !cpoerrors.IsNotFound(err) {
			return err
		}
	}

	if loadbalancer != nil {
		if err := lbaas.deleteLoadBalancer(ctx, loadbalancer, service); err != nil {
			return err
		}
	}

	// Delete the Security Group. We're doing that even if `manage-security-groups` is disabled to make sure we don't
	// orphan created SGs even if CPO got reconfigured.
	if err := lbaas.ensureSecurityGroupDeleted(ctx, service); err != nil {
		return err
	}

	return nil
}

// filterNodes uses node labels to filter the nodes that should be targeted by the LB,
// ensuring that all the labels provided in an annotation are present on the nodes
func filterNodes(nodes []*corev1.Node, filterLabels map[string]string) []*corev1.Node {
	if len(filterLabels) == 0 {
		return nodes
	}

	filteredNodes := make([]*corev1.Node, 0, len(nodes))
	for _, node := range nodes {
		if matchNodeLabels(node, filterLabels) {
			filteredNodes = append(filteredNodes, node)
		}
	}

	return filteredNodes
}

// matchNodeLabels checks if a node has all the labels in filterLabels with matching values
func matchNodeLabels(node *corev1.Node, filterLabels map[string]string) bool {
	if node == nil || len(node.Labels) == 0 {
		return false
	}

	for k, v := range filterLabels {
		if nodeLabelValue, ok := node.Labels[k]; !ok || (v != "" && nodeLabelValue != v) {
			return false
		}
	}

	return true
}

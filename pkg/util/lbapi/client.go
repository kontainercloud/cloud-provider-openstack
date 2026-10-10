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

// Package lbapi is the gRPC client of the loadbalancer.v1.LoadBalancerService
// that openstack-cloud-controller-manager uses to manage load balancers for
// Services of type LoadBalancer.
package lbapi

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	lbv1 "k8s.io/cloud-provider-openstack/pkg/util/lbapi/gen"
)

const (
	// DefaultTimeout bounds a single RPC attempt.
	DefaultTimeout = 30 * time.Second
	// DefaultRetryMax is the number of retries after the first attempt.
	DefaultRetryMax = 3
	// DefaultRetryDelay is the base of the exponential backoff between attempts.
	DefaultRetryDelay = 100 * time.Millisecond

	maxRetryDelay = 5 * time.Second
)

// Interface is the subset of the load balancer service the cloud provider
// uses. It exists so that callers can be tested with a fake.
type Interface interface {
	CreateLoadBalancer(ctx context.Context, req *lbv1.CreateLoadBalancerRequest) (*lbv1.CreateLoadBalancerResponse, error)
	GetLoadBalancer(ctx context.Context, req *lbv1.GetLoadBalancerRequest) (*lbv1.GetLoadBalancerResponse, error)
	ListLoadBalancers(ctx context.Context, req *lbv1.ListLoadBalancersRequest) (*lbv1.ListLoadBalancersResponse, error)
	UpdateLoadBalancer(ctx context.Context, req *lbv1.UpdateLoadBalancerRequest) (*lbv1.UpdateLoadBalancerResponse, error)
	DeleteLoadBalancer(ctx context.Context, req *lbv1.DeleteLoadBalancerRequest) (*lbv1.DeleteLoadBalancerResponse, error)
	AllocateFloatingIp(ctx context.Context, req *lbv1.AllocateFloatingIpRequest) (*lbv1.AllocateFloatingIpResponse, error)
	ReleaseFloatingIp(ctx context.Context, req *lbv1.ReleaseFloatingIpRequest) (*lbv1.ReleaseFloatingIpResponse, error)
	AuthenticatedPing(ctx context.Context, req *lbv1.AuthenticatedPingRequest) (*lbv1.AuthenticatedPingResponse, error)
}

// Config holds the connection settings of the load balancer service.
type Config struct {
	// ServerAddr is "host:port" or "http://host:port".
	ServerAddr string
	// APIKey is sent as a bearer token on every RPC. No credential is sent
	// when it is empty.
	APIKey string
	// Timeout bounds a single RPC attempt. Each retry gets a fresh Timeout.
	Timeout time.Duration
	// RetryMax is the number of retries after the first attempt. 0 means no
	// retry, a negative value the default.
	RetryMax int
	// RetryDelay is the base of the exponential backoff between attempts.
	RetryDelay time.Duration
	// DialOpts are appended to the default dial options.
	DialOpts []grpc.DialOption
}

type apiKeyCreds struct {
	apiKey string
}

func (c apiKeyCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": "Bearer " + c.apiKey,
	}, nil
}

// RequireTransportSecurity is false because the load balancer service only
// serves cleartext HTTP/2.
func (c apiKeyCreds) RequireTransportSecurity() bool {
	return false
}

// Client is a retrying client of the load balancer service.
type Client struct {
	conn   *grpc.ClientConn
	client lbv1.LoadBalancerServiceClient
	config Config
}

var _ Interface = &Client{}

// NewClient creates a client. The connection is established lazily, on the
// first RPC.
func NewClient(config Config) (*Client, error) {
	if config.Timeout <= 0 {
		config.Timeout = DefaultTimeout
	}
	if config.RetryMax < 0 {
		config.RetryMax = DefaultRetryMax
	}
	if config.RetryDelay <= 0 {
		config.RetryDelay = DefaultRetryDelay
	}

	target, err := normalizeServerAddr(config.ServerAddr)
	if err != nil {
		return nil, err
	}

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	if config.APIKey != "" {
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(apiKeyCreds{apiKey: config.APIKey}))
	}
	dialOpts = append(dialOpts, config.DialOpts...)

	conn, err := grpc.NewClient(target, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create load balancer service client for %q: %w", target, err)
	}

	return &Client{
		conn:   conn,
		client: lbv1.NewLoadBalancerServiceClient(conn),
		config: config,
	}, nil
}

// normalizeServerAddr turns a configured address into a target gRPC can dial.
// gRPC expects "host:port"; given "http://host:port" it treats the whole string
// as an address and the dial fails with "too many colons in address", which
// says nothing about the real problem. An http:// URL is a natural thing to
// write in a config file, so accept it and strip it down.
//
// Anything carrying a scheme gRPC understands itself - dns:, unix:,
// passthrough: - is handed through untouched.
func normalizeServerAddr(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", fmt.Errorf("load balancer service address is empty")
	}
	// Targets naming a gRPC resolver are handed through untouched, also in
	// their forms without "//" such as unix:/run/lb.sock or dns:lb-api:8080.
	for _, scheme := range []string{"dns:", "unix:", "unix-abstract:", "passthrough:"} {
		if strings.HasPrefix(addr, scheme) {
			return addr, nil
		}
	}
	if !strings.Contains(addr, "://") {
		// "host:port/" or "host:port/path": only host:port can be dialed.
		hostPort, _, _ := strings.Cut(addr, "/")
		// Without a port gRPC would dial 443, where nothing answers in plaintext.
		if host, port, err := net.SplitHostPort(hostPort); err != nil || host == "" || port == "" {
			return "", fmt.Errorf("invalid load balancer service address %q: use host:port or http://host:port", addr)
		}
		return hostPort, nil
	}

	u, err := url.Parse(addr)
	if err != nil {
		return "", fmt.Errorf("invalid load balancer service address %q: %w", addr, err)
	}

	switch u.Scheme {
	case "http":
	case "https":
		// Silently dialing plaintext would betray what the address asked for.
		return "", fmt.Errorf("invalid load balancer service address %q: TLS is not supported, use http:// or host:port", addr)
	default:
		return addr, nil
	}

	// Hostname/Port rather than Host, so a bracketed IPv6 literal survives.
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("invalid load balancer service address %q: no host", addr)
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	return net.JoinHostPort(host, port), nil
}

// Close closes the underlying connection.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// IsRetryable reports whether err is a transient failure worth retrying.
func IsRetryable(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
		return true
	default:
		return false
	}
}

// IsNotFound reports whether err is a gRPC NotFound error.
func IsNotFound(err error) bool {
	return err != nil && status.Code(err) == codes.NotFound
}

// invoke runs one RPC with retries. Every attempt gets its own deadline: calls
// are made with WaitForReady(true), so without one they would block for as
// long as the caller's context lives - which for the service controller is
// effectively forever - whenever the server is unreachable.
func invoke[Req, Resp any](ctx context.Context, c *Client, operation string, req Req,
	fn func(context.Context, Req, ...grpc.CallOption) (Resp, error),
) (Resp, error) {
	var zero Resp
	var lastErr error
	for attempt := 0; attempt <= c.config.RetryMax; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(c.backoff(attempt)):
			}
		}

		attemptCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
		resp, err := fn(attemptCtx, req, grpc.WaitForReady(true))
		cancel()
		if err == nil {
			return resp, nil
		}

		// The caller gave up (or its deadline passed); retrying is pointless
		// and would misreport the reason.
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}

		if !IsRetryable(err) {
			return zero, fmt.Errorf("%s: %w", operation, err)
		}
		lastErr = err
	}

	return zero, fmt.Errorf("%s failed after %d attempts: %w", operation, c.config.RetryMax+1, lastErr)
}

func (c *Client) backoff(attempt int) time.Duration {
	delay := c.config.RetryDelay * time.Duration(1<<uint(attempt))
	if delay <= 0 || delay > maxRetryDelay {
		return maxRetryDelay
	}
	delay += time.Duration(rand.Int63n(int64(delay)/2 + 1))
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	return delay
}

// CreateLoadBalancer creates a load balancer. The request must carry an
// idempotency key, which makes a retried create return the existing one.
func (c *Client) CreateLoadBalancer(ctx context.Context, req *lbv1.CreateLoadBalancerRequest) (*lbv1.CreateLoadBalancerResponse, error) {
	return invoke(ctx, c, "CreateLoadBalancer", req, c.client.CreateLoadBalancer)
}

// GetLoadBalancer returns a load balancer by ID.
func (c *Client) GetLoadBalancer(ctx context.Context, req *lbv1.GetLoadBalancerRequest) (*lbv1.GetLoadBalancerResponse, error) {
	return invoke(ctx, c, "GetLoadBalancer", req, c.client.GetLoadBalancer)
}

// ListLoadBalancers returns one page of load balancer summaries.
func (c *Client) ListLoadBalancers(ctx context.Context, req *lbv1.ListLoadBalancersRequest) (*lbv1.ListLoadBalancersResponse, error) {
	return invoke(ctx, c, "ListLoadBalancers", req, c.client.ListLoadBalancers)
}

// UpdateLoadBalancer patches a load balancer.
func (c *Client) UpdateLoadBalancer(ctx context.Context, req *lbv1.UpdateLoadBalancerRequest) (*lbv1.UpdateLoadBalancerResponse, error) {
	return invoke(ctx, c, "UpdateLoadBalancer", req, c.client.UpdateLoadBalancer)
}

// DeleteLoadBalancer marks a load balancer for deletion.
func (c *Client) DeleteLoadBalancer(ctx context.Context, req *lbv1.DeleteLoadBalancerRequest) (*lbv1.DeleteLoadBalancerResponse, error) {
	return invoke(ctx, c, "DeleteLoadBalancer", req, c.client.DeleteLoadBalancer)
}

// AllocateFloatingIp assigns a floating IP to a load balancer.
func (c *Client) AllocateFloatingIp(ctx context.Context, req *lbv1.AllocateFloatingIpRequest) (*lbv1.AllocateFloatingIpResponse, error) {
	return invoke(ctx, c, "AllocateFloatingIp", req, c.client.AllocateFloatingIp)
}

// ReleaseFloatingIp removes the floating IP from a load balancer.
func (c *Client) ReleaseFloatingIp(ctx context.Context, req *lbv1.ReleaseFloatingIpRequest) (*lbv1.ReleaseFloatingIpResponse, error) {
	return invoke(ctx, c, "ReleaseFloatingIp", req, c.client.ReleaseFloatingIp)
}

// AuthenticatedPing checks that the service is reachable and, when the
// response reports auth_enforced, that the configured API key is accepted.
func (c *Client) AuthenticatedPing(ctx context.Context, req *lbv1.AuthenticatedPingRequest) (*lbv1.AuthenticatedPingResponse, error) {
	return invoke(ctx, c, "AuthenticatedPing", req, c.client.AuthenticatedPing)
}

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

package lbapi

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	lbv1 "k8s.io/cloud-provider-openstack/pkg/util/lbapi/gen"
)

const testLBID = "7b0d3c1e-3f0a-4c58-9d55-1c0a3d2f4b6a"

// fakeServer answers GetLoadBalancer with the queued errors in order, then
// succeeds. It records how often it was called and the authorization header.
type fakeServer struct {
	lbv1.UnimplementedLoadBalancerServiceServer

	mu     sync.Mutex
	errs   []error
	calls  int
	auth   []string
	delay  time.Duration
	delete *lbv1.DeleteLoadBalancerRequest
}

func (s *fakeServer) GetLoadBalancer(ctx context.Context, req *lbv1.GetLoadBalancerRequest) (*lbv1.GetLoadBalancerResponse, error) {
	s.mu.Lock()
	s.calls++
	md, _ := metadata.FromIncomingContext(ctx)
	s.auth = append(s.auth, md.Get("authorization")...)
	var err error
	if len(s.errs) > 0 {
		err = s.errs[0]
		s.errs = s.errs[1:]
	}
	delay := s.delay
	s.mu.Unlock()

	if delay > 0 {
		select {
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		case <-time.After(delay):
		}
	}
	if err != nil {
		return nil, err
	}
	return &lbv1.GetLoadBalancerResponse{LoadBalancer: &lbv1.LoadBalancer{Id: req.GetId(), State: lbv1.State_STATE_READY}}, nil
}

func (s *fakeServer) DeleteLoadBalancer(_ context.Context, req *lbv1.DeleteLoadBalancerRequest) (*lbv1.DeleteLoadBalancerResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delete = req
	return &lbv1.DeleteLoadBalancerResponse{Id: req.GetId(), State: lbv1.State_STATE_DELETING}, nil
}

func (s *fakeServer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func newTestClient(t *testing.T, srv *fakeServer, config Config) *Client {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()
	lbv1.RegisterLoadBalancerServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	config.ServerAddr = "passthrough:///bufnet"
	config.DialOpts = []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = time.Millisecond
	}

	c, err := NewClient(config)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClientSuccess(t *testing.T) {
	srv := &fakeServer{}
	c := newTestClient(t, srv, Config{})

	resp, err := c.GetLoadBalancer(context.Background(), &lbv1.GetLoadBalancerRequest{Id: testLBID})
	require.NoError(t, err)
	assert.Equal(t, testLBID, resp.GetLoadBalancer().GetId())
	assert.Equal(t, 1, srv.callCount())
}

func TestClientRetriesTransientErrors(t *testing.T) {
	srv := &fakeServer{errs: []error{
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "still down"),
	}}
	c := newTestClient(t, srv, Config{RetryMax: 3})

	_, err := c.GetLoadBalancer(context.Background(), &lbv1.GetLoadBalancerRequest{Id: testLBID})
	require.NoError(t, err)
	assert.Equal(t, 3, srv.callCount())
}

func TestClientGivesUpAfterRetryMax(t *testing.T) {
	srv := &fakeServer{errs: []error{
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"),
	}}
	c := newTestClient(t, srv, Config{RetryMax: 2})

	_, err := c.GetLoadBalancer(context.Background(), &lbv1.GetLoadBalancerRequest{Id: testLBID})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	assert.Contains(t, err.Error(), "failed after 3 attempts")
	assert.Equal(t, 3, srv.callCount())
}

func TestClientDoesNotRetryPermanentErrors(t *testing.T) {
	for _, code := range []codes.Code{
		codes.NotFound, codes.InvalidArgument, codes.Unauthenticated,
		codes.PermissionDenied, codes.FailedPrecondition, codes.Internal,
	} {
		t.Run(code.String(), func(t *testing.T) {
			srv := &fakeServer{errs: []error{status.Error(code, "nope")}}
			c := newTestClient(t, srv, Config{RetryMax: 3})

			_, err := c.GetLoadBalancer(context.Background(), &lbv1.GetLoadBalancerRequest{Id: testLBID})
			require.Error(t, err)
			assert.Equal(t, code, status.Code(err))
			assert.Equal(t, code == codes.NotFound, IsNotFound(err))
			assert.Equal(t, 1, srv.callCount())
		})
	}
}

func TestClientBoundsEachAttempt(t *testing.T) {
	srv := &fakeServer{delay: time.Second}
	c := newTestClient(t, srv, Config{Timeout: 20 * time.Millisecond, RetryMax: 1})

	start := time.Now()
	_, err := c.GetLoadBalancer(context.Background(), &lbv1.GetLoadBalancerRequest{Id: testLBID})
	require.Error(t, err)
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
	assert.Less(t, time.Since(start), 900*time.Millisecond)
}

func TestClientHonoursCallerContext(t *testing.T) {
	srv := &fakeServer{errs: []error{status.Error(codes.Unavailable, "down")}}
	c := newTestClient(t, srv, Config{RetryMax: 5, RetryDelay: time.Hour})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.GetLoadBalancer(ctx, &lbv1.GetLoadBalancerRequest{Id: testLBID})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, srv.callCount())
}

func TestClientBearerToken(t *testing.T) {
	t.Run("sent when the key is set", func(t *testing.T) {
		srv := &fakeServer{}
		c := newTestClient(t, srv, Config{APIKey: "s3cret"})

		_, err := c.GetLoadBalancer(context.Background(), &lbv1.GetLoadBalancerRequest{Id: testLBID})
		require.NoError(t, err)
		assert.Equal(t, []string{"Bearer s3cret"}, srv.auth)
	})

	t.Run("absent when the key is empty", func(t *testing.T) {
		srv := &fakeServer{}
		c := newTestClient(t, srv, Config{})

		_, err := c.GetLoadBalancer(context.Background(), &lbv1.GetLoadBalancerRequest{Id: testLBID})
		require.NoError(t, err)
		assert.Empty(t, srv.auth)
	})
}

func TestClientDeletePassesPreserveFloatingIP(t *testing.T) {
	srv := &fakeServer{}
	c := newTestClient(t, srv, Config{})

	resp, err := c.DeleteLoadBalancer(context.Background(), &lbv1.DeleteLoadBalancerRequest{Id: testLBID, PreserveFloatingIp: true})
	require.NoError(t, err)
	assert.Equal(t, lbv1.State_STATE_DELETING, resp.GetState())
	assert.True(t, srv.delete.GetPreserveFloatingIp())
}

func TestBackoffIsCapped(t *testing.T) {
	c := &Client{config: Config{RetryDelay: DefaultRetryDelay}}
	for attempt := 1; attempt < 80; attempt++ {
		d := c.backoff(attempt)
		assert.Positive(t, d)
		assert.LessOrEqual(t, d, maxRetryDelay)
	}
}

func TestNormalizeServerAddr(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"lb-api:8080", "lb-api:8080"},
		{" lb-api:8080 ", "lb-api:8080"},
		{"http://lb-api:8080", "lb-api:8080"},
		{"http://lb-api", "lb-api:80"},
		{"http://lb-api:8080/", "lb-api:8080"},
		{"http://lb-api:8080/v1/path?x=1", "lb-api:8080"},
		{"HTTP://lb-api:8080", "lb-api:8080"},
		{"lb-api:8080/", "lb-api:8080"},
		{"10.2.0.29:8080/api", "10.2.0.29:8080"},
		{"http://[fd00::1]:8080", "[fd00::1]:8080"},
		{"dns:///lb-api:8080", "dns:///lb-api:8080"},
		{"passthrough:///bufnet", "passthrough:///bufnet"},
		{"unix:/var/run/lb-api.sock", "unix:/var/run/lb-api.sock"},
		{"unix:///var/run/lb-api.sock", "unix:///var/run/lb-api.sock"},
		{"dns:lb-api:8080", "dns:lb-api:8080"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := normalizeServerAddr(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeServerAddrRejects(t *testing.T) {
	for _, in := range []string{"", "   ", "https://lb-api:8443", "http://:8080", "lb-api", "lb-api/", ":8080"} {
		t.Run(in, func(t *testing.T) {
			_, err := normalizeServerAddr(in)
			assert.Error(t, err)
		})
	}
}

func TestNewClientRejectsBadAddress(t *testing.T) {
	_, err := NewClient(Config{ServerAddr: "https://lb-api:8443"})
	assert.Error(t, err)
}

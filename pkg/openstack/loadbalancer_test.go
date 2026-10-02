package openstack

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/rules"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"

	"k8s.io/cloud-provider-openstack/pkg/util"
	cpoerrors "k8s.io/cloud-provider-openstack/pkg/util/errors"
	"k8s.io/cloud-provider-openstack/pkg/util/lbapi"
	lbv1 "k8s.io/cloud-provider-openstack/pkg/util/lbapi/gen"
)

type testGetRulesToCreateAndDelete struct {
	testName      string
	wantedRules   []rules.CreateOpts
	existingRules []rules.SecGroupRule
	toCreate      []rules.CreateOpts
	toDelete      []rules.SecGroupRule
}

func TestGetRulesToCreateAndDelete(t *testing.T) {
	tests := []testGetRulesToCreateAndDelete{
		{
			testName:      "Empty elements",
			wantedRules:   []rules.CreateOpts{},
			existingRules: []rules.SecGroupRule{},
			toCreate:      []rules.CreateOpts{},
			toDelete:      []rules.SecGroupRule{},
		},
		{
			testName: "Removal of default egress SG rules",
			wantedRules: []rules.CreateOpts{
				{
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					PortRangeMax:   123,
					PortRangeMin:   123,
					Protocol:       "TCP",
					RemoteIPPrefix: "10.0.0.0/8",
				},
			},
			existingRules: []rules.SecGroupRule{
				{
					ID:             "bar",
					Direction:      "egress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					RemoteIPPrefix: "0.0.0.0/0",
				}, {
					ID:             "baz",
					Direction:      "egress",
					EtherType:      "IPv6",
					SecGroupID:     "foo",
					RemoteIPPrefix: "::/0",
				},
			},
			toCreate: []rules.CreateOpts{
				{
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					PortRangeMax:   123,
					PortRangeMin:   123,
					Protocol:       "TCP",
					RemoteIPPrefix: "10.0.0.0/8",
				},
			},
			toDelete: []rules.SecGroupRule{
				{
					ID:             "bar",
					Direction:      "egress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					RemoteIPPrefix: "0.0.0.0/0",
				}, {
					ID:             "baz",
					Direction:      "egress",
					EtherType:      "IPv6",
					SecGroupID:     "foo",
					RemoteIPPrefix: "::/0",
				},
			},
		},
		{
			testName: "Protocol case mismatch",
			wantedRules: []rules.CreateOpts{
				{
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					PortRangeMax:   123,
					PortRangeMin:   123,
					Protocol:       "TCP",
					RemoteIPPrefix: "10.0.0.0/8",
				},
			},
			existingRules: []rules.SecGroupRule{
				{
					ID:             "bar",
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					PortRangeMax:   123,
					PortRangeMin:   123,
					Protocol:       "tcp",
					RemoteIPPrefix: "10.0.0.0/8",
				},
			},
			toCreate: []rules.CreateOpts{},
			toDelete: []rules.SecGroupRule{},
		},
		{
			testName: "changing a port number",
			wantedRules: []rules.CreateOpts{
				{
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					PortRangeMax:   124,
					PortRangeMin:   124,
					Protocol:       "TCP",
					RemoteIPPrefix: "10.0.0.0/8",
				},
			},
			existingRules: []rules.SecGroupRule{
				{
					ID:             "bar",
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					RemoteIPPrefix: "10.0.0.0/8",
					PortRangeMax:   123,
					PortRangeMin:   123,
				},
			},
			toCreate: []rules.CreateOpts{
				{
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					PortRangeMax:   124,
					PortRangeMin:   124,
					Protocol:       "TCP",
					RemoteIPPrefix: "10.0.0.0/8",
				},
			},
			toDelete: []rules.SecGroupRule{
				{
					ID:             "bar",
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					RemoteIPPrefix: "10.0.0.0/8",
					PortRangeMax:   123,
					PortRangeMin:   123,
				},
			},
		},
		{
			testName: "changing the CIDR",
			wantedRules: []rules.CreateOpts{
				{
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					PortRangeMax:   123,
					PortRangeMin:   123,
					Protocol:       "TCP",
					RemoteIPPrefix: "10.0.0.0/24",
				},
			},
			existingRules: []rules.SecGroupRule{
				{
					ID:             "bar",
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					RemoteIPPrefix: "10.0.0.0/8",
					PortRangeMax:   123,
					PortRangeMin:   123,
				},
			},
			toCreate: []rules.CreateOpts{
				{
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					PortRangeMax:   123,
					PortRangeMin:   123,
					Protocol:       "TCP",
					RemoteIPPrefix: "10.0.0.0/24",
				},
			},
			toDelete: []rules.SecGroupRule{
				{
					ID:             "bar",
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					RemoteIPPrefix: "10.0.0.0/8",
					PortRangeMax:   123,
					PortRangeMin:   123,
				},
			},
		},
		{
			testName:    "wiping all rules",
			wantedRules: []rules.CreateOpts{},
			existingRules: []rules.SecGroupRule{
				{
					ID:             "bar",
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					RemoteIPPrefix: "10.0.0.0/8",
					PortRangeMax:   123,
					PortRangeMin:   123,
				},
				{
					ID:             "bar",
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					RemoteIPPrefix: "10.0.0.0/8",
					PortRangeMax:   124,
					PortRangeMin:   124,
				},
				{
					ID:             "bar",
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					RemoteIPPrefix: "10.0.0.0/8",
					PortRangeMax:   125,
					PortRangeMin:   125,
				},
			},
			toCreate: []rules.CreateOpts{},
			toDelete: []rules.SecGroupRule{
				{
					ID:             "bar",
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					RemoteIPPrefix: "10.0.0.0/8",
					PortRangeMax:   123,
					PortRangeMin:   123,
				},
				{
					ID:             "bar",
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					RemoteIPPrefix: "10.0.0.0/8",
					PortRangeMax:   124,
					PortRangeMin:   124,
				},
				{
					ID:             "bar",
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					RemoteIPPrefix: "10.0.0.0/8",
					PortRangeMax:   125,
					PortRangeMin:   125,
				},
			},
		},
		{
			testName: "several rules for an empty SG",
			wantedRules: []rules.CreateOpts{
				{
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					PortRangeMax:   123,
					PortRangeMin:   123,
					Protocol:       "TCP",
					RemoteIPPrefix: "10.0.0.0/8",
				}, {
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					PortRangeMax:   124,
					PortRangeMin:   124,
					Protocol:       "TCP",
					RemoteIPPrefix: "10.0.10.0/24",
				}, {
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					PortRangeMax:   124,
					PortRangeMin:   124,
					Protocol:       "UDP",
					RemoteIPPrefix: "10.0.12.0/24",
				},
			},
			existingRules: []rules.SecGroupRule{},
			toCreate: []rules.CreateOpts{
				{
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					PortRangeMax:   123,
					PortRangeMin:   123,
					Protocol:       "TCP",
					RemoteIPPrefix: "10.0.0.0/8",
				}, {
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					PortRangeMax:   124,
					PortRangeMin:   124,
					Protocol:       "TCP",
					RemoteIPPrefix: "10.0.10.0/24",
				}, {
					Direction:      "ingress",
					EtherType:      "IPv4",
					SecGroupID:     "foo",
					PortRangeMax:   124,
					PortRangeMin:   124,
					Protocol:       "UDP",
					RemoteIPPrefix: "10.0.12.0/24",
				},
			},
			toDelete: []rules.SecGroupRule{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			toCreate, toDelete := getRulesToCreateAndDelete(tt.wantedRules, tt.existingRules)
			assert.ElementsMatch(t, tt.toCreate, toCreate)
			assert.ElementsMatch(t, tt.toDelete, toDelete)
		})
	}
}

func Test_getIntFromServiceAnnotation(t *testing.T) {
	type args struct {
		service        *corev1.Service
		annotationKey  string
		defaultSetting int
	}
	tests := []struct {
		name string
		args args
		want int
	}{
		{
			name: "return default setting if no service annotation",
			args: args{
				defaultSetting: 1,
				annotationKey:  "bar",
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{"foo": "2"},
					},
				},
			},
			want: 1,
		},
		{
			name: "return annotation key if it exists in service annotation",
			args: args{
				defaultSetting: 1,
				annotationKey:  "foo",
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{"foo": "2"},
					},
				},
			},
			want: 2,
		},
		{
			name: "return default setting if key isn't valid integer",
			args: args{
				defaultSetting: 1,
				annotationKey:  "foo",
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{"foo": "bar"},
					},
				},
			},
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, getIntFromServiceAnnotation(tt.args.service, tt.args.annotationKey, tt.args.defaultSetting))
		})
	}
}

func TestLbaasV2_GetLoadBalancerName(t *testing.T) {
	lbaas := &LbaasV2{}

	type testArgs struct {
		ctx         context.Context
		clusterName string
		service     *corev1.Service
	}
	tests := []struct {
		name     string
		testArgs testArgs
		expected string
	}{
		{
			name: "valid input with short name",
			testArgs: testArgs{
				ctx:         context.Background(),
				clusterName: "my-valid-cluster",
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Namespace: "valid-cluster-namespace",
						Name:      "valid-name",
					},
				},
			},
			expected: "kube_service_my-valid-cluster_valid-cluster-namespace_valid-name",
		},
		{
			name: "input that surpass value maximum length",
			testArgs: testArgs{
				ctx:         context.Background(),
				clusterName: "a-longer-valid-cluster",
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Namespace: "a-longer-valid-cluster-namespace",
						Name:      "a-longer-valid-name-for-the-load-balance-name-to-test-if-the-length-of-value-is-longer-than-required-maximum-length-random-addition-hardcode-number-to-make-it-above-length-255-at-the-end-yeah-so-the-rest-is-additional-input",
					},
				},
			},
			expected: "kube_service_a-longer-valid-cluster_a-longer-valid-cluster-namespace_a-longer-valid-name-for-the-load-balance-name-to-test-if-the-length-of-value-is-longer-than-required-maximum-length-random-addition-hardcode-number-to-make-it-above-length-255-at-the-end",
		},
		{
			name: "empty input",
			testArgs: testArgs{
				ctx:         context.Background(),
				clusterName: "",
				service:     &corev1.Service{},
			},
			expected: "kube_service___",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lbaas.GetLoadBalancerName(tt.testArgs.ctx, tt.testArgs.clusterName, tt.testArgs.service)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func Test_getSecurityGroupName(t *testing.T) {
	tests := []struct {
		name     string
		service  *corev1.Service
		expected string
	}{
		{
			name: "regular test security group name and length",
			service: &corev1.Service{
				ObjectMeta: v1.ObjectMeta{
					UID:       "12345",
					Namespace: "security-group-namespace",
					Name:      "security-group-name",
				},
			},
			expected: "lb-sg-12345-security-group-namespace-security-group-name",
		},
		{
			name: "security group name longer than 255 byte",
			service: &corev1.Service{
				ObjectMeta: v1.ObjectMeta{
					UID:       "12345678-90ab-cdef-0123-456789abcdef",
					Namespace: "security-group-longer-test-namespace",
					Name:      "security-group-longer-test-service-name-with-more-than-255-byte-this-test-should-be-longer-than-255-i-need-that-ijiojohoo-afhwefkbfk-jwebfwbifwbewifobiu-efbiobfoiqwebi-the-end-e-end-pardon-the-long-string-i-really-apologize-if-this-is-a-bad-thing-to-do",
				},
			},
			expected: "lb-sg-12345678-90ab-cdef-0123-456789abcdef-security-group-longer-test-namespace-security-group-longer-test-service-name-with-more-than-255-byte-this-test-should-be-longer-than-255-i-need-that-ijiojohoo-afhwefkbfk-jwebfwbifwbewifobiu-efbiobfoiqwebi-the-end",
		},
		{
			name: "test the security group name with all empty param",
			service: &corev1.Service{
				ObjectMeta: v1.ObjectMeta{},
			},
			expected: "lb-sg---",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := getSecurityGroupName(test.service)

			assert.Equal(t, test.expected, got)
		})
	}
}

func Test_getBoolFromServiceAnnotation(t *testing.T) {
	type testargs struct {
		service        *corev1.Service
		annotationKey  string
		defaultSetting bool
	}
	tests := []struct {
		name     string
		testargs testargs
		want     bool
	}{
		{
			name: "Return default setting if no service annotation",
			testargs: testargs{
				annotationKey:  "bar",
				defaultSetting: true,
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{"foo": "false"},
					},
				},
			},
			want: true,
		},
		{
			name: "Return annotation key if it exists in service annotation (true)",
			testargs: testargs{
				annotationKey:  "foo",
				defaultSetting: false,
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{"foo": "true"},
					},
				},
			},
			want: true,
		},
		{
			name: "Return annotation key if it exists in service annotation (false)",
			testargs: testargs{
				annotationKey:  "foo",
				defaultSetting: true,
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{"foo": "false"},
					},
				},
			},
			want: false,
		},
		{
			name: "Return default setting if key isn't a valid boolean value",
			testargs: testargs{
				annotationKey:  "foo",
				defaultSetting: true,
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{"foo": "invalid"},
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getBoolFromServiceAnnotation(tt.testargs.service, tt.testargs.annotationKey, tt.testargs.defaultSetting)
			if got != tt.want {
				t.Errorf("getBoolFromServiceAnnotation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLbaasV2_updateServiceAnnotations(t *testing.T) {
	service := &corev1.Service{
		ObjectMeta: v1.ObjectMeta{
			Annotations: nil,
		},
	}

	lbaas := LbaasV2{}
	lbaas.updateServiceAnnotation(service, "key1", "value1")

	serviceAnnotations := make([]map[string]string, 0)
	for key, value := range service.Annotations {
		serviceAnnotations = append(serviceAnnotations, map[string]string{key: value})
	}

	expectedAnnotations := []map[string]string{
		{"key1": "value1"},
	}

	assert.ElementsMatch(t, expectedAnnotations, serviceAnnotations)
}

func Test_getStringFromServiceAnnotation(t *testing.T) {
	type testArgs struct {
		service        *corev1.Service
		annotationKey  string
		defaultSetting string
	}

	tests := []struct {
		name     string
		testArgs testArgs
		expected string
	}{
		{
			name: "enter empty arguments",
			testArgs: testArgs{
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{},
				},
				annotationKey:  "",
				defaultSetting: "",
			},
			expected: "",
		},
		{
			name: "enter valid arguments with annotations",
			testArgs: testArgs{
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Namespace:   "service-namespace",
						Name:        "service-name",
						Annotations: map[string]string{"annotationKey": "annotation-Value"},
					},
				},
				annotationKey:  "annotationKey",
				defaultSetting: "default-setting",
			},
			expected: "annotation-Value",
		},
		{
			name: "valid arguments without annotations",
			testArgs: testArgs{
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Namespace: "service-namespace",
						Name:      "service-name",
					},
				},
				annotationKey:  "annotationKey",
				defaultSetting: "default-setting",
			},
			expected: "default-setting",
		},
		{
			name: "enter argument without default-setting",
			testArgs: testArgs{
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Namespace:   "service-namespace",
						Name:        "service-name",
						Annotations: map[string]string{"annotationKey": "annotation-Value"},
					},
				},
				annotationKey:  "annotationKey",
				defaultSetting: "",
			},
			expected: "annotation-Value",
		},
		{
			name: "enter argument without annotation and default-setting",
			testArgs: testArgs{
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Namespace: "service-namespace",
						Name:      "service-name",
					},
				},
				annotationKey:  "annotationKey",
				defaultSetting: "",
			},
			expected: "",
		},
		{
			name: "enter argument with a non-existing annotationKey with default setting",
			testArgs: testArgs{
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Namespace:   "service-namespace",
						Name:        "service-name",
						Annotations: map[string]string{"annotationKey": "annotation-Value"},
					},
				},
				annotationKey:  "invalid-annotationKey",
				defaultSetting: "default-setting",
			},
			expected: "default-setting",
		},
		{
			name: "enter argument with a non-existing annotationKey without a default setting",
			testArgs: testArgs{
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Namespace:   "service-namespace",
						Name:        "service-name",
						Annotations: map[string]string{"annotationKey": "annotation-Value"},
					},
				},
				annotationKey:  "invalid-annotationKey",
				defaultSetting: "",
			},
			expected: "",
		},
		{
			name: "no name-space and service name but valid annotations",
			testArgs: testArgs{
				service: &corev1.Service{
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{"annotationKey": "annotation-Value"},
					},
				},
				annotationKey:  "annotationKey",
				defaultSetting: "default-setting",
			},
			expected: "annotation-Value",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := getStringFromServiceAnnotation(test.testArgs.service, test.testArgs.annotationKey, test.testArgs.defaultSetting)

			assert.Equal(t, test.expected, got)
		})
	}
}

func Test_nodeAddressForLB(t *testing.T) {
	type testArgs struct {
		node              *corev1.Node
		preferredIPFamily corev1.IPFamily
	}

	tests := []struct {
		name        string
		testArgs    testArgs
		expect      string
		expectedErr error
	}{
		{
			name: "Empty Address with IPv4 protocol family ",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{},
					},
				},
				preferredIPFamily: corev1.IPv4Protocol,
			},
			expect:      "",
			expectedErr: cpoerrors.ErrNoAddressFound,
		},
		{
			name: "Empty Address with IPv6 protocol family ",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{},
					},
				},
				preferredIPFamily: corev1.IPv6Protocol,
			},
			expect:      "",
			expectedErr: cpoerrors.ErrNoAddressFound,
		},
		{
			name: "valid address with IPv4 protocol family",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "192.168.1.1",
							},
						},
					},
				},
				preferredIPFamily: corev1.IPv4Protocol,
			},
			expect:      "192.168.1.1",
			expectedErr: nil,
		},
		{
			name: "valid address with IPv6 protocol family",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
							},
						},
					},
				},
				preferredIPFamily: corev1.IPv6Protocol,
			},
			expect:      "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			expectedErr: nil,
		},
		{
			name: "multiple IPv4 address",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeExternalIP,
								Address: "192.168.1.2",
							},
							{
								Type:    corev1.NodeInternalIP,
								Address: "192.168.1.1",
							},
						},
					},
				},
				preferredIPFamily: corev1.IPv4Protocol,
			},
			expect:      "192.168.1.1",
			expectedErr: nil,
		},
		{
			name: "multiple IPv6 address",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeExternalIP,
								Address: "2001:0db8:85a3:3333:1111:8a2e:9999:8888",
							},
							{
								Type:    corev1.NodeInternalIP,
								Address: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
							},
						},
					},
				},
				preferredIPFamily: corev1.IPv6Protocol,
			},
			expect:      "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			expectedErr: nil,
		},
		{
			name: "multiple mix addresses expecting IPv6 response",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "192.168.1.1",
							},
							{
								Type:    corev1.NodeInternalIP,
								Address: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
							},
						},
					},
				},
				preferredIPFamily: corev1.IPv6Protocol,
			},
			expect:      "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			expectedErr: nil,
		},
		{
			name: "multiple mix addresses expecting IPv4 response",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeExternalIP,
								Address: "2009:0db8:85a3:0003:0001:8a2e:0370:9999",
							},

							{
								Type:    corev1.NodeInternalIP,
								Address: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
							},

							{
								Type:    corev1.NodeExternalIP,
								Address: "2001:0db8:85a3:0000:1111:8a2e:9798:7334",
							},

							{
								Type:    corev1.NodeInternalIP,
								Address: "192.168.1.1",
							},

							{
								Type:    corev1.NodeExternalIP,
								Address: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
							},
						},
					},
				},
				preferredIPFamily: corev1.IPv4Protocol,
			},
			expect:      "192.168.1.1",
			expectedErr: nil,
		},
		{
			name: "single valid IPv4 address without preferred valid specification",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "192.168.1.1",
							},
						},
					},
				},
			},
			expect:      "192.168.1.1",
			expectedErr: nil,
		},
		{
			name: "single valid IPv6 address without preferred valid specification",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
							},
						},
					},
				},
			},
			expect:      "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			expectedErr: nil,
		},
		{
			name: "multiple valid IPv6 address without preferred valid specification",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
							},
							{
								Type:    corev1.NodeInternalIP,
								Address: "192.168.0.1",
							},
							{
								Type:    corev1.NodeInternalIP,
								Address: "2001:0db8:85a3:1111:2222:8a2e:6869:7334",
							},
						},
					},
				},
			},
			expect:      "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			expectedErr: nil,
		},
		{
			name: "invalid IPv4 address specification",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
							},
						},
					},
				},
				preferredIPFamily: corev1.IPv4Protocol,
			},
			expect:      "",
			expectedErr: cpoerrors.ErrNoAddressFound,
		},
		{
			name: "invalid IPv6 address specification",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "192.168.1.1",
							},
						},
					},
				},
				preferredIPFamily: corev1.IPv6Protocol,
			},
			expect:      "",
			expectedErr: cpoerrors.ErrNoAddressFound,
		},
		{
			name: "Ignore NodeExternalDNS address with IPv4 protocol family",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeExternalDNS,
								Address: "example.com",
							},
						},
					},
				},
				preferredIPFamily: corev1.IPv4Protocol,
			},
			expect:      "",
			expectedErr: cpoerrors.ErrNoAddressFound,
		},
		{
			name: "Ignore NodeExternalDNS address with IPv6 protocol family",
			testArgs: testArgs{
				node: &corev1.Node{
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeExternalDNS,
								Address: "example.com",
							},
						},
					},
				},
				preferredIPFamily: corev1.IPv6Protocol,
			},
			expect:      "",
			expectedErr: cpoerrors.ErrNoAddressFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := nodeAddressForLB(test.testArgs.node, test.testArgs.preferredIPFamily)
			if test.expectedErr != nil {
				assert.EqualError(t, err, test.expectedErr.Error())
			} else {
				assert.NoError(t, test.expectedErr, err)
			}

			assert.Equal(t, test.expect, got)
		})
	}
}

func TestFilterNodes(t *testing.T) {
	tests := []struct {
		name           string
		nodeLabels     map[string]string
		service        *corev1.Service
		annotationKey  string
		defaultSetting map[string]string
		nodeFiltered   bool
	}{
		{
			name:       "when no filter is provided, node should be filtered",
			nodeLabels: map[string]string{"k1": "v1"},
			service: &corev1.Service{
				ObjectMeta: v1.ObjectMeta{},
			},
			annotationKey:  ServiceAnnotationLoadBalancerNodeSelector,
			defaultSetting: make(map[string]string),
			nodeFiltered:   true,
		},
		{
			name:       "when all key-value filters match, node should be filtered",
			nodeLabels: map[string]string{"k1": "v1", "k2": "v2"},
			service: &corev1.Service{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{ServiceAnnotationLoadBalancerNodeSelector: "k1=v1,k2=v2"},
				},
			},
			annotationKey:  ServiceAnnotationLoadBalancerNodeSelector,
			defaultSetting: make(map[string]string),
			nodeFiltered:   true,
		},
		{
			name:       "when all key-value filters match and a key value contains equals sign, node should be filtered",
			nodeLabels: map[string]string{"k1": "v1", "k2": "v2=true"},
			service: &corev1.Service{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{ServiceAnnotationLoadBalancerNodeSelector: "k1=v1,k2=v2=true"},
				},
			},
			annotationKey:  ServiceAnnotationLoadBalancerNodeSelector,
			defaultSetting: make(map[string]string),
			nodeFiltered:   true,
		},
		{
			name:       "when all just-key filter match, node should be filtered",
			nodeLabels: map[string]string{"k1": "v1", "k2": "v2"},
			service: &corev1.Service{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{ServiceAnnotationLoadBalancerNodeSelector: "k1,k2"},
				},
			},
			annotationKey:  ServiceAnnotationLoadBalancerNodeSelector,
			defaultSetting: make(map[string]string),
			nodeFiltered:   true,
		},
		{
			name:       "when some filters do not match, node should not be filtered",
			nodeLabels: map[string]string{"k1": "v1"},
			service: &corev1.Service{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{ServiceAnnotationLoadBalancerNodeSelector: " k1=v1, k2 "},
				},
			},
			annotationKey:  ServiceAnnotationLoadBalancerNodeSelector,
			defaultSetting: make(map[string]string),
			nodeFiltered:   false,
		},
		{
			name:       "when no filter matches, node should not be filtered",
			nodeLabels: map[string]string{"k1": "v1", "k2": "v2"},
			service: &corev1.Service{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{ServiceAnnotationLoadBalancerNodeSelector: "k3=v3"},
				},
			},
			annotationKey:  ServiceAnnotationLoadBalancerNodeSelector,
			defaultSetting: make(map[string]string),
			nodeFiltered:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := &corev1.Node{}
			node.Labels = test.nodeLabels

			// TODO: add testArgs
			targetNodeLabels := getKeyValueFromServiceAnnotation(test.service, ServiceAnnotationLoadBalancerNodeSelector, "")

			nodes := []*corev1.Node{node}
			filteredNodes := filterNodes(nodes, targetNodeLabels)

			if test.nodeFiltered {
				assert.Equal(t, nodes, filteredNodes)
			} else {
				assert.Empty(t, filteredNodes)
			}
		})
	}
}

// fakeLBClient is an in-memory stand-in of the load balancer service. It
// records the requests it receives and mimics the behaviour of the service
// that the reconcile logic relies on: idempotent creates, asynchronous
// deletes and paged lists.
type fakeLBClient struct {
	lbapi.Interface

	lbs  map[string]*lbv1.LoadBalancer
	keys map[string]string

	// createState is the state a new load balancer is created in.
	createState lbv1.State
	// fipState is the state of the floating IP of a new load balancer.
	fipState lbv1.FipState
	// purgeOnDelete removes a load balancer as soon as it is deleted,
	// otherwise it lingers in DELETING.
	purgeOnDelete bool
	// pageSize caps the pages of ListLoadBalancers regardless of the request.
	pageSize int

	createErr error
	getErr    error
	listErr   error
	deleteErr error

	createReqs   []*lbv1.CreateLoadBalancerRequest
	listReqs     []*lbv1.ListLoadBalancersRequest
	updateReqs   []*lbv1.UpdateLoadBalancerRequest
	deleteReqs   []*lbv1.DeleteLoadBalancerRequest
	allocateReqs []*lbv1.AllocateFloatingIpRequest
	releaseReqs  []*lbv1.ReleaseFloatingIpRequest
}

func newFakeLBClient() *fakeLBClient {
	return &fakeLBClient{
		lbs:           map[string]*lbv1.LoadBalancer{},
		keys:          map[string]string{},
		createState:   lbv1.State_STATE_READY,
		fipState:      lbv1.FipState_FIP_STATE_ACTIVE,
		purgeOnDelete: true,
	}
}

func (f *fakeLBClient) add(lb *lbv1.LoadBalancer) *lbv1.LoadBalancer {
	f.lbs[lb.Id] = lb
	return lb
}

func (f *fakeLBClient) CreateLoadBalancer(_ context.Context, req *lbv1.CreateLoadBalancerRequest) (*lbv1.CreateLoadBalancerResponse, error) {
	f.createReqs = append(f.createReqs, req)
	if f.createErr != nil {
		return nil, f.createErr
	}
	if id, ok := f.keys[req.IdempotencyKey]; ok {
		if lb, ok := f.lbs[id]; ok {
			return &lbv1.CreateLoadBalancerResponse{Id: lb.Id, State: lb.State}, nil
		}
	}

	lb := &lbv1.LoadBalancer{
		Id:    fmt.Sprintf("00000000-0000-4000-8000-%012d", len(f.keys)+1),
		Name:  req.Name,
		Spec:  req.Spec,
		State: f.createState,
		Ip:    "10.0.0.5",
	}
	if req.Fip != nil {
		lb.FipEnabled = true
		lb.FipNetworkId = req.GetFipNetworkId()
		lb.FipState = f.fipState
		if f.fipState == lbv1.FipState_FIP_STATE_ACTIVE {
			lb.Fip = "203.0.113.10"
			if req.GetFip() != "" {
				lb.Fip = req.GetFip()
			}
		}
	}
	f.lbs[lb.Id] = lb
	f.keys[req.IdempotencyKey] = lb.Id
	return &lbv1.CreateLoadBalancerResponse{Id: lb.Id, State: lb.State}, nil
}

func (f *fakeLBClient) GetLoadBalancer(_ context.Context, req *lbv1.GetLoadBalancerRequest) (*lbv1.GetLoadBalancerResponse, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	lb, ok := f.lbs[req.Id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "load balancer %q not found", req.Id)
	}
	return &lbv1.GetLoadBalancerResponse{LoadBalancer: lb}, nil
}

func (f *fakeLBClient) ListLoadBalancers(_ context.Context, req *lbv1.ListLoadBalancersRequest) (*lbv1.ListLoadBalancersResponse, error) {
	f.listReqs = append(f.listReqs, req)
	if f.listErr != nil {
		return nil, f.listErr
	}

	ids := make([]string, 0, len(f.lbs))
	for id, lb := range f.lbs {
		if lb.GetSpec().GetTenantId() == req.TenantId && lb.State != lbv1.State_STATE_DELETED {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	offset := 0
	if req.PageToken != "" {
		offset, _ = strconv.Atoi(req.PageToken)
	}
	size := int(req.PageSize)
	if f.pageSize > 0 {
		size = f.pageSize
	}
	end := min(offset+size, len(ids))

	resp := &lbv1.ListLoadBalancersResponse{}
	for _, id := range ids[offset:end] {
		lb := f.lbs[id]
		resp.LoadBalancers = append(resp.LoadBalancers, &lbv1.LoadBalancerSummary{Id: lb.Id, Name: lb.Name, State: lb.State})
	}
	if end < len(ids) {
		resp.NextPageToken = strconv.Itoa(end)
	}
	return resp, nil
}

func (f *fakeLBClient) UpdateLoadBalancer(_ context.Context, req *lbv1.UpdateLoadBalancerRequest) (*lbv1.UpdateLoadBalancerResponse, error) {
	f.updateReqs = append(f.updateReqs, req)
	return &lbv1.UpdateLoadBalancerResponse{Id: req.Id, State: lbv1.State_STATE_UPDATING}, nil
}

func (f *fakeLBClient) DeleteLoadBalancer(_ context.Context, req *lbv1.DeleteLoadBalancerRequest) (*lbv1.DeleteLoadBalancerResponse, error) {
	f.deleteReqs = append(f.deleteReqs, req)
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	lb, ok := f.lbs[req.Id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "load balancer %q not found", req.Id)
	}
	if f.purgeOnDelete {
		delete(f.lbs, req.Id)
	} else {
		lb.State = lbv1.State_STATE_DELETING
	}
	return &lbv1.DeleteLoadBalancerResponse{Id: req.Id, State: lbv1.State_STATE_DELETING}, nil
}

func (f *fakeLBClient) AllocateFloatingIp(_ context.Context, req *lbv1.AllocateFloatingIpRequest) (*lbv1.AllocateFloatingIpResponse, error) {
	f.allocateReqs = append(f.allocateReqs, req)
	return &lbv1.AllocateFloatingIpResponse{Id: req.Id, FipState: lbv1.FipState_FIP_STATE_PROVISIONING}, nil
}

func (f *fakeLBClient) ReleaseFloatingIp(_ context.Context, req *lbv1.ReleaseFloatingIpRequest) (*lbv1.ReleaseFloatingIpResponse, error) {
	f.releaseReqs = append(f.releaseReqs, req)
	return &lbv1.ReleaseFloatingIpResponse{Id: req.Id, FipState: lbv1.FipState_FIP_STATE_REMOVING}, nil
}

const (
	testTenantID          = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	testNetworkID         = "11111111-1111-4111-8111-111111111111"
	testSubnetID          = "22222222-2222-4222-8222-222222222222"
	testFloatingNetworkID = "33333333-3333-4333-8333-333333333333"
	testServiceUID        = "44444444-4444-4444-8444-444444444444"
	testLBClusterName     = "kubernetes"
	testLBName            = "kube_service_kubernetes_default_web"
)

// newFakeNeutron serves a Neutron API in which no security group exists,
// which is all the reconcile needs when manage-security-groups is off.
func newFakeNeutron(t *testing.T) *gophercloud.ServiceClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			t.Errorf("unexpected Neutron request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"security_groups": []}`))
	}))
	t.Cleanup(srv.Close)
	return &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       srv.URL + "/",
	}
}

func newTestService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: v1.ObjectMeta{Name: "web", Namespace: "default", UID: testServiceUID},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer,
			Ports: []corev1.ServicePort{
				{Name: "http", Protocol: corev1.ProtocolTCP, Port: 80, NodePort: 30080},
				{Name: "dns", Protocol: corev1.ProtocolUDP, Port: 53, NodePort: 30053},
			},
		},
	}
}

func newTestNodes(addresses ...string) []*corev1.Node {
	nodes := make([]*corev1.Node, 0, len(addresses))
	for i, address := range addresses {
		nodes = append(nodes, &corev1.Node{
			ObjectMeta: v1.ObjectMeta{Name: fmt.Sprintf("node-%d", i)},
			Status: corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: address}},
			},
		})
	}
	return nodes
}

// newTestLbaas returns the provider wired to the fake service and to a fake
// API server holding the Service, with the state polling sped up.
func newTestLbaas(t *testing.T, client *fakeLBClient, service *corev1.Service) (*LbaasV2, *fake.Clientset) {
	t.Helper()

	initDelay, activeSteps, deleteSteps := waitLoadBalancerInitDelay, waitLoadBalancerActiveSteps, waitLoadBalancerDeleteSteps
	waitLoadBalancerInitDelay, waitLoadBalancerActiveSteps, waitLoadBalancerDeleteSteps = time.Millisecond, 3, 3
	t.Cleanup(func() {
		waitLoadBalancerInitDelay, waitLoadBalancerActiveSteps, waitLoadBalancerDeleteSteps = initDelay, activeSteps, deleteSteps
	})

	kclient := fake.NewSimpleClientset(service.DeepCopy())
	return &LbaasV2{LoadBalancer{
		network:  newFakeNeutron(t),
		lb:       client,
		tenantID: testTenantID,
		opts: LoadBalancerOpts{
			Enabled:               true,
			NetworkID:             testNetworkID,
			SubnetID:              testSubnetID,
			FloatingNetworkID:     testFloatingNetworkID,
			LBMethod:              "ROUND_ROBIN",
			CreateMonitor:         true,
			MonitorDelay:          util.MyDuration{Duration: 5 * time.Second},
			MonitorTimeout:        util.MyDuration{Duration: 3 * time.Second},
			MonitorMaxRetries:     1,
			MonitorMaxRetriesDown: 3,
		},
		kclient:       kclient,
		eventRecorder: record.NewFakeRecorder(100),
	}}, kclient
}

func storedAnnotations(t *testing.T, kclient *fake.Clientset) map[string]string {
	t.Helper()
	svc, err := kclient.CoreV1().Services("default").Get(context.Background(), "web", v1.GetOptions{})
	require.NoError(t, err)
	return svc.Annotations
}

func TestCreateIdempotencyKey(t *testing.T) {
	svc := newTestService()

	first := createIdempotencyKey(svc, 1)
	assert.Equal(t, testServiceUID, first, "the first attempt uses the Service UID")
	assert.Equal(t, first, createIdempotencyKey(svc, 0))

	second := createIdempotencyKey(svc, 2)
	assert.NotEqual(t, first, second)
	assert.Equal(t, second, createIdempotencyKey(svc, 2), "a key is stable within an attempt")
	assert.NotEqual(t, second, createIdempotencyKey(svc, 3))
	_, err := uuid.Parse(second)
	assert.NoError(t, err)

	svc.UID = "not-a-uuid"
	key := createIdempotencyKey(svc, 1)
	_, err = uuid.Parse(key)
	assert.NoError(t, err, "the key is a UUID even when the UID is not")
	assert.NotEqual(t, key, createIdempotencyKey(svc, 2))
}

func TestParseAlgorithm(t *testing.T) {
	tests := []struct {
		in      string
		want    lbv1.Algorithm
		wantErr bool
	}{
		{"", lbv1.Algorithm_ALGORITHM_ROUND_ROBIN, false},
		{"ROUND_ROBIN", lbv1.Algorithm_ALGORITHM_ROUND_ROBIN, false},
		{"round_robin", lbv1.Algorithm_ALGORITHM_ROUND_ROBIN, false},
		{"LEAST_CONNECTIONS", lbv1.Algorithm_ALGORITHM_LEAST_REQUEST, false},
		{"SOURCE_IP", lbv1.Algorithm_ALGORITHM_CONSISTENT_HASH, false},
		{"SOURCE_IP_PORT", lbv1.Algorithm_ALGORITHM_CONSISTENT_HASH, false},
		{"FASTEST", lbv1.Algorithm_ALGORITHM_UNSPECIFIED, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseAlgorithm(tt.in)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCheckServicePorts(t *testing.T) {
	tests := []struct {
		name    string
		ports   []corev1.ServicePort
		wantErr string
	}{
		{name: "no ports", wantErr: "no service ports"},
		{name: "tcp and udp on different ports", ports: []corev1.ServicePort{
			{Protocol: corev1.ProtocolTCP, Port: 80}, {Protocol: corev1.ProtocolUDP, Port: 53},
		}},
		{name: "protocol defaults to tcp", ports: []corev1.ServicePort{{Port: 80}}},
		{name: "sctp", ports: []corev1.ServicePort{{Protocol: corev1.ProtocolSCTP, Port: 80}}, wantErr: "not supported"},
		{name: "tcp and udp on the same port", ports: []corev1.ServicePort{
			{Protocol: corev1.ProtocolTCP, Port: 53}, {Protocol: corev1.ProtocolUDP, Port: 53},
		}, wantErr: "only once"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkServicePorts(&corev1.Service{Spec: corev1.ServiceSpec{Ports: tt.ports}})
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestBuildEndpoints(t *testing.T) {
	port := corev1.ServicePort{Port: 80, NodePort: 30080}
	nodes := newTestNodes("10.0.0.12", "10.0.0.11", "169.254.10.1", "10.0.0.11")
	nodes = append(nodes, &corev1.Node{ObjectMeta: v1.ObjectMeta{Name: "no-address"}})

	got := buildEndpoints(port, nodes, &serviceConfig{})

	assert.Equal(t, []*lbv1.BackendRef{
		{Ip: "10.0.0.11", Port: 30080},
		{Ip: "10.0.0.12", Port: 30080},
	}, got, "endpoints are sorted, deduplicated and without unroutable addresses")
}

func TestBuildListeners(t *testing.T) {
	svcConf := &serviceConfig{
		algorithm:                   lbv1.Algorithm_ALGORITHM_ROUND_ROBIN,
		enableMonitor:               true,
		healthMonitorDelay:          5,
		healthMonitorTimeout:        3,
		healthMonitorMaxRetries:     1,
		healthMonitorMaxRetriesDown: 3,
	}
	nodes := newTestNodes("10.0.0.11", "10.0.0.12")

	t.Run("one listener per port", func(t *testing.T) {
		got, err := buildListeners(newTestService(), nodes, svcConf)
		require.NoError(t, err)
		require.Len(t, got, 2)

		tcp := got[0]
		assert.Equal(t, int32(80), tcp.Port)
		assert.Equal(t, lbv1.Protocol_PROTOCOL_TCP, tcp.Protocol)
		require.Len(t, tcp.Rules, 1)
		assert.Empty(t, tcp.Rules[0].Matches)
		assert.Equal(t, lbv1.Algorithm_ALGORITHM_ROUND_ROBIN, tcp.Rules[0].Algorithm)
		require.Len(t, tcp.Rules[0].Backends, 1)
		assert.Equal(t, int32(1), tcp.Rules[0].Backends[0].Weight)
		assert.Equal(t, []*lbv1.BackendRef{
			{Ip: "10.0.0.11", Port: 30080},
			{Ip: "10.0.0.12", Port: 30080},
		}, tcp.Rules[0].Backends[0].Endpoints)
		assert.Equal(t, &lbv1.HealthMonitor{Interval: 5, Timeout: 3, HealthyThreshold: 1, UnhealthyThreshold: 3}, tcp.Rules[0].HealthMonitor)

		udp := got[1]
		assert.Equal(t, int32(53), udp.Port)
		assert.Equal(t, lbv1.Protocol_PROTOCOL_UDP, udp.Protocol)
		assert.Nil(t, udp.Rules[0].HealthMonitor, "UDP listeners get no health monitor")
		assert.Equal(t, int32(30053), udp.Rules[0].Backends[0].Endpoints[0].Port)
	})

	t.Run("monitor disabled", func(t *testing.T) {
		conf := *svcConf
		conf.enableMonitor = false
		got, err := buildListeners(newTestService(), nodes, &conf)
		require.NoError(t, err)
		assert.Nil(t, got[0].Rules[0].HealthMonitor)
	})

	t.Run("no usable node address", func(t *testing.T) {
		_, err := buildListeners(newTestService(), newTestNodes("169.254.1.1"), svcConf)
		assert.ErrorContains(t, err, "no usable node address")
	})

	t.Run("no node port", func(t *testing.T) {
		svc := newTestService()
		svc.Spec.Ports[0].NodePort = 0
		_, err := buildListeners(svc, nodes, svcConf)
		assert.ErrorContains(t, err, "no node port")
	})
}

func TestMakeSvcConf(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		check       func(t *testing.T, svcConf *serviceConfig)
		wantErr     string
	}{
		{
			name: "defaults from the cloud config",
			check: func(t *testing.T, svcConf *serviceConfig) {
				assert.Equal(t, testTenantID, svcConf.tenantID)
				assert.Equal(t, 0, svcConf.connLimit)
				assert.Equal(t, lbv1.Algorithm_ALGORITHM_ROUND_ROBIN, svcConf.algorithm)
				assert.True(t, svcConf.enableMonitor)
				assert.Equal(t, 5, svcConf.healthMonitorDelay)
				assert.Equal(t, 3, svcConf.healthMonitorTimeout)
			},
		},
		{
			name: "annotations override the cloud config",
			annotations: map[string]string{
				ServiceAnnotationLoadBalancerConnLimit:           "500",
				ServiceAnnotationLoadBalancerLbMethod:            "LEAST_CONNECTIONS",
				ServiceAnnotationLoadBalancerHealthMonitorDelay:  "20",
				ServiceAnnotationLoadBalancerNodeSelector:        "pool=web",
				ServiceAnnotationLoadBalancerID:                  "00000000-0000-4000-8000-000000000009",
				ServiceAnnotationLoadBalancerEnableHealthMonitor: "true",
			},
			check: func(t *testing.T, svcConf *serviceConfig) {
				assert.Equal(t, 500, svcConf.connLimit)
				assert.Equal(t, lbv1.Algorithm_ALGORITHM_LEAST_REQUEST, svcConf.algorithm)
				assert.Equal(t, 20, svcConf.healthMonitorDelay)
				assert.Equal(t, map[string]string{"pool": "web"}, svcConf.nodeSelectors)
				assert.Equal(t, "00000000-0000-4000-8000-000000000009", svcConf.lbID)
			},
		},
		{
			name:        "octavia's unlimited connection limit",
			annotations: map[string]string{ServiceAnnotationLoadBalancerConnLimit: "-1"},
			check: func(t *testing.T, svcConf *serviceConfig) {
				assert.Equal(t, 0, svcConf.connLimit)
			},
		},
		{
			name:        "unknown lb-method",
			annotations: map[string]string{ServiceAnnotationLoadBalancerLbMethod: "FASTEST"},
			wantErr:     "unknown lb-method",
		},
		{
			name:        "monitor timeout not below delay",
			annotations: map[string]string{ServiceAnnotationLoadBalancerHealthMonitorTimeout: "5"},
			wantErr:     "must be less than delay",
		},
		{
			name: "invalid monitor is fine when the monitor is off",
			annotations: map[string]string{
				ServiceAnnotationLoadBalancerHealthMonitorTimeout: "5",
				ServiceAnnotationLoadBalancerEnableHealthMonitor:  "false",
			},
			check: func(t *testing.T, svcConf *serviceConfig) {
				assert.False(t, svcConf.enableMonitor)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService()
			svc.Annotations = tt.annotations
			lbaas, _ := newTestLbaas(t, newFakeLBClient(), svc)

			svcConf := new(serviceConfig)
			err := lbaas.makeSvcConf("default/web", svc, svcConf)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			tt.check(t, svcConf)
		})
	}
}

func TestMakeSvcConfWithoutTenant(t *testing.T) {
	svc := newTestService()
	lbaas, _ := newTestLbaas(t, newFakeLBClient(), svc)
	lbaas.tenantID = ""

	assert.ErrorContains(t, lbaas.makeSvcConf("default/web", svc, new(serviceConfig)), "tenant-id")
}

func TestGetLoadbalancerByName(t *testing.T) {
	client := newFakeLBClient()
	client.pageSize = 2
	spec := &lbv1.LoadBalancerSpec{TenantId: testTenantID}
	client.add(&lbv1.LoadBalancer{Id: "00000000-0000-4000-8000-00000000000a", Name: "other-1", Spec: spec, State: lbv1.State_STATE_READY})
	client.add(&lbv1.LoadBalancer{Id: "00000000-0000-4000-8000-00000000000b", Name: testLBName, Spec: spec, State: lbv1.State_STATE_DELETING})
	client.add(&lbv1.LoadBalancer{Id: "00000000-0000-4000-8000-00000000000c", Name: "other-2", Spec: spec, State: lbv1.State_STATE_READY})
	client.add(&lbv1.LoadBalancer{Id: "00000000-0000-4000-8000-00000000000d", Name: testLBName, Spec: &lbv1.LoadBalancerSpec{TenantId: "another-tenant"}, State: lbv1.State_STATE_READY})

	_, err := getLoadbalancerByName(context.Background(), client, testTenantID, testLBName)
	assert.Equal(t, cpoerrors.ErrNotFound, err, "a load balancer being deleted and one of another tenant do not match")

	want := client.add(&lbv1.LoadBalancer{Id: "00000000-0000-4000-8000-00000000000e", Name: testLBName, Spec: spec, State: lbv1.State_STATE_READY})
	client.listReqs = nil
	got, err := getLoadbalancerByName(context.Background(), client, testTenantID, testLBName)
	require.NoError(t, err)
	assert.Equal(t, want.Id, got.Id)
	require.Len(t, client.listReqs, 2, "all pages are read")
	assert.Equal(t, listLoadBalancersPageSize, client.listReqs[0].PageSize)
	assert.Equal(t, testTenantID, client.listReqs[0].TenantId)

	client.add(&lbv1.LoadBalancer{Id: "00000000-0000-4000-8000-00000000000f", Name: testLBName, Spec: spec, State: lbv1.State_STATE_PENDING})
	_, err = getLoadbalancerByName(context.Background(), client, testTenantID, testLBName)
	assert.Equal(t, cpoerrors.ErrMultipleResults, err)

	client.listErr = status.Error(codes.Unavailable, "down")
	_, err = getLoadbalancerByName(context.Background(), client, testTenantID, testLBName)
	assert.Equal(t, codes.Unavailable, status.Code(err), "a failed lookup is an error, not a miss")
}

func TestEnsureLoadBalancerCreates(t *testing.T) {
	client := newFakeLBClient()
	svc := newTestService()
	lbaas, kclient := newTestLbaas(t, client, svc)

	lbStatus, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, newTestNodes("10.0.0.11", "10.0.0.12"))
	require.NoError(t, err)

	require.Len(t, client.createReqs, 1)
	req := client.createReqs[0]
	assert.Equal(t, testLBName, req.Name)
	assert.Equal(t, testServiceUID, req.IdempotencyKey)
	assert.Equal(t, testTenantID, req.Spec.TenantId)
	assert.Equal(t, testNetworkID, req.Spec.NetworkId)
	assert.Equal(t, testSubnetID, req.Spec.SubnetId)
	assert.True(t, req.Spec.SecurityGroupDisabled)
	assert.True(t, req.Spec.QosPolicyDisabled)
	assert.Len(t, req.Spec.Listeners, 2)
	require.NotNil(t, req.Fip, "the fip field must be present for a floating IP to be allocated")
	assert.Empty(t, req.GetFip())
	assert.Equal(t, testFloatingNetworkID, req.GetFipNetworkId())
	assert.Empty(t, client.updateReqs)

	require.Len(t, lbStatus.Ingress, 1)
	assert.Equal(t, "203.0.113.10", lbStatus.Ingress[0].IP, "an external Service is reached at the floating IP")

	annotations := storedAnnotations(t, kclient)
	assert.Equal(t, "00000000-0000-4000-8000-000000000001", annotations[ServiceAnnotationLoadBalancerID])
	assert.Equal(t, "1", annotations[ServiceAnnotationLoadBalancerCreateAttempt])
	assert.Equal(t, "203.0.113.10", annotations[ServiceAnnotationLoadBalancerAddress])
}

func TestEnsureLoadBalancerCreatesInternal(t *testing.T) {
	client := newFakeLBClient()
	svc := newTestService()
	svc.Annotations = map[string]string{ServiceAnnotationLoadBalancerInternal: "true"}
	lbaas, _ := newTestLbaas(t, client, svc)
	lbaas.opts.SecurityGroupIDs = "55555555-5555-4555-8555-555555555555, 66666666-6666-4666-8666-666666666666"
	lbaas.opts.QoSPolicyID = "77777777-7777-4777-8777-777777777777"
	lbaas.opts.VPCCIDR = "10.0.0.0/16"

	lbStatus, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, newTestNodes("10.0.0.11"))
	require.NoError(t, err)

	req := client.createReqs[0]
	assert.Nil(t, req.Fip)
	assert.Nil(t, req.FipNetworkId)
	assert.False(t, req.Spec.SecurityGroupDisabled)
	assert.Equal(t, []string{"55555555-5555-4555-8555-555555555555", "66666666-6666-4666-8666-666666666666"}, req.Spec.SecurityGroupIds)
	assert.False(t, req.Spec.QosPolicyDisabled)
	assert.Equal(t, "77777777-7777-4777-8777-777777777777", req.Spec.QosPolicyId)
	assert.Equal(t, "10.0.0.0/16", req.Spec.VpcCidr)
	assert.Equal(t, "10.0.0.5", lbStatus.Ingress[0].IP, "an internal Service is reached at the VIP")
}

func TestEnsureLoadBalancerRequestedFloatingIP(t *testing.T) {
	client := newFakeLBClient()
	svc := newTestService()
	svc.Spec.LoadBalancerIP = "203.0.113.77"
	lbaas, _ := newTestLbaas(t, client, svc)

	lbStatus, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, newTestNodes("10.0.0.11"))
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.77", client.createReqs[0].GetFip())
	assert.Equal(t, "203.0.113.77", lbStatus.Ingress[0].IP)
}

func TestEnsureLoadBalancerIsIdempotent(t *testing.T) {
	client := newFakeLBClient()
	svc := newTestService()
	lbaas, kclient := newTestLbaas(t, client, svc)
	nodes := newTestNodes("10.0.0.11")

	_, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, nodes)
	require.NoError(t, err)

	t.Run("with the ID annotation", func(t *testing.T) {
		stored, err := kclient.CoreV1().Services("default").Get(context.Background(), "web", v1.GetOptions{})
		require.NoError(t, err)

		_, err = lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, stored, nodes)
		require.NoError(t, err)
		assert.Len(t, client.createReqs, 1)
	})

	t.Run("found by name when the annotation was lost", func(t *testing.T) {
		_, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, newTestService(), nodes)
		require.NoError(t, err)
		assert.Len(t, client.createReqs, 1)
		assert.Len(t, client.lbs, 1)
	})
}

func TestEnsureLoadBalancerNotReady(t *testing.T) {
	t.Run("new load balancer stays pending", func(t *testing.T) {
		client := newFakeLBClient()
		client.createState = lbv1.State_STATE_PENDING
		svc := newTestService()
		lbaas, kclient := newTestLbaas(t, client, svc)

		_, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, newTestNodes("10.0.0.11"))
		assert.ErrorContains(t, err, "timeout waiting")
		assert.Equal(t, "00000000-0000-4000-8000-000000000001", storedAnnotations(t, kclient)[ServiceAnnotationLoadBalancerID],
			"the ID is saved although the reconcile failed")
	})

	t.Run("new load balancer fails", func(t *testing.T) {
		client := newFakeLBClient()
		client.createState = lbv1.State_STATE_FAILED
		svc := newTestService()
		lbaas, _ := newTestLbaas(t, client, svc)

		_, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, newTestNodes("10.0.0.11"))
		assert.ErrorContains(t, err, "failed to provision")
		assert.Empty(t, client.deleteReqs, "a failed load balancer is left alone")
	})

	t.Run("existing load balancer is failed", func(t *testing.T) {
		client := newFakeLBClient()
		svc := newTestService()
		lbaas, _ := newTestLbaas(t, client, svc)
		nodes := newTestNodes("10.0.0.11")
		_, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, nodes)
		require.NoError(t, err)

		lb := client.lbs["00000000-0000-4000-8000-000000000001"]
		lb.State = lbv1.State_STATE_FAILED
		lb.Error = "no port available"

		_, err = lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, nodes)
		assert.ErrorContains(t, err, "not READY")
		assert.ErrorContains(t, err, "no port available")
		assert.Len(t, client.createReqs, 1)
		assert.Empty(t, client.deleteReqs)
		assert.Empty(t, client.updateReqs)
	})

	t.Run("floating IP not active yet", func(t *testing.T) {
		client := newFakeLBClient()
		client.fipState = lbv1.FipState_FIP_STATE_PROVISIONING
		svc := newTestService()
		lbaas, _ := newTestLbaas(t, client, svc)

		_, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, newTestNodes("10.0.0.11"))
		assert.ErrorContains(t, err, "timeout waiting")
	})
}

func TestEnsureLoadBalancerRecreatesWithNewKey(t *testing.T) {
	t.Run("ID annotation points at a load balancer that is gone", func(t *testing.T) {
		client := newFakeLBClient()
		svc := newTestService()
		svc.Annotations = map[string]string{
			ServiceAnnotationLoadBalancerID:            "00000000-0000-4000-8000-0000000000ff",
			ServiceAnnotationLoadBalancerCreateAttempt: "1",
		}
		lbaas, kclient := newTestLbaas(t, client, svc)

		_, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, newTestNodes("10.0.0.11"))
		require.NoError(t, err)

		require.Len(t, client.createReqs, 1)
		assert.Equal(t, createIdempotencyKey(svc, 2), client.createReqs[0].IdempotencyKey)
		annotations := storedAnnotations(t, kclient)
		assert.Equal(t, "2", annotations[ServiceAnnotationLoadBalancerCreateAttempt])
		assert.Equal(t, "00000000-0000-4000-8000-000000000001", annotations[ServiceAnnotationLoadBalancerID])
	})

	t.Run("create is answered with a load balancer being deleted", func(t *testing.T) {
		client := newFakeLBClient()
		spec := &lbv1.LoadBalancerSpec{TenantId: testTenantID}
		client.add(&lbv1.LoadBalancer{Id: "00000000-0000-4000-8000-0000000000aa", Name: testLBName, Spec: spec, State: lbv1.State_STATE_DELETING})
		client.keys[testServiceUID] = "00000000-0000-4000-8000-0000000000aa"
		svc := newTestService()
		lbaas, kclient := newTestLbaas(t, client, svc)
		nodes := newTestNodes("10.0.0.11")

		_, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, nodes)
		assert.ErrorContains(t, err, "still being deleted")
		annotations := storedAnnotations(t, kclient)
		assert.Equal(t, "2", annotations[ServiceAnnotationLoadBalancerCreateAttempt])
		assert.NotContains(t, annotations, ServiceAnnotationLoadBalancerID)

		_, err = lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, nodes)
		require.NoError(t, err, "the next reconcile creates with the next key")
		assert.Equal(t, createIdempotencyKey(svc, 2), client.createReqs[1].IdempotencyKey)
	})

	t.Run("create fails on a leftover key", func(t *testing.T) {
		client := newFakeLBClient()
		client.createErr = status.Error(codes.Internal, "insert load balancer: duplicate entry")
		svc := newTestService()
		lbaas, kclient := newTestLbaas(t, client, svc)

		_, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, newTestNodes("10.0.0.11"))
		require.Error(t, err)
		assert.Equal(t, "2", storedAnnotations(t, kclient)[ServiceAnnotationLoadBalancerCreateAttempt])
	})

	t.Run("create fails for another reason", func(t *testing.T) {
		client := newFakeLBClient()
		client.createErr = status.Error(codes.InvalidArgument, "bad listener")
		svc := newTestService()
		lbaas, kclient := newTestLbaas(t, client, svc)

		_, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, newTestNodes("10.0.0.11"))
		assert.ErrorContains(t, err, "bad listener")
		assert.NotContains(t, storedAnnotations(t, kclient), ServiceAnnotationLoadBalancerCreateAttempt)
	})
}

func TestEnsureLoadBalancerRejectsBadServices(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(svc *corev1.Service)
		nodes   []*corev1.Node
		wantErr string
	}{
		{name: "no nodes", mutate: func(*corev1.Service) {}, wantErr: "no available nodes"},
		{
			name:    "ipv6",
			mutate:  func(svc *corev1.Service) { svc.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv6Protocol} },
			nodes:   newTestNodes("10.0.0.11"),
			wantErr: "IPv6",
		},
		{
			name:    "same port for tcp and udp",
			mutate:  func(svc *corev1.Service) { svc.Spec.Ports[1].Port = 80 },
			nodes:   newTestNodes("10.0.0.11"),
			wantErr: "only once",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFakeLBClient()
			svc := newTestService()
			tt.mutate(svc)
			lbaas, _ := newTestLbaas(t, client, svc)

			_, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, tt.nodes)
			assert.ErrorContains(t, err, tt.wantErr)
			assert.Empty(t, client.createReqs)
		})
	}
}

func TestEnsureLoadBalancerRejectsForeignLoadBalancer(t *testing.T) {
	const lbID = "00000000-0000-4000-8000-0000000000bb"
	client := newFakeLBClient()
	client.add(&lbv1.LoadBalancer{
		Id: lbID, Name: "kube_service_kubernetes_default_other", State: lbv1.State_STATE_READY, Ip: "10.0.0.9",
		Spec: &lbv1.LoadBalancerSpec{TenantId: testTenantID},
	})
	svc := newTestService()
	svc.Annotations = map[string]string{ServiceAnnotationLoadBalancerID: lbID}
	lbaas, _ := newTestLbaas(t, client, svc)

	_, err := lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, newTestNodes("10.0.0.11"))
	assert.ErrorContains(t, err, "sharing a load balancer is not supported")
	assert.Empty(t, client.createReqs)
}

func TestEnsureFloatingIP(t *testing.T) {
	const lbID = "00000000-0000-4000-8000-000000000001"

	t.Run("internal Service releases the floating IP", func(t *testing.T) {
		client := newFakeLBClient()
		svc := newTestService()
		lbaas, _ := newTestLbaas(t, client, svc)
		lb := &lbv1.LoadBalancer{Id: lbID, Ip: "10.0.0.5", FipEnabled: true, Fip: "203.0.113.10", FipState: lbv1.FipState_FIP_STATE_ACTIVE}

		addr, err := lbaas.ensureFloatingIP(context.Background(), svc, lb, &serviceConfig{internal: true})
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.5", addr)
		require.Len(t, client.releaseReqs, 1)
		assert.Equal(t, lbID, client.releaseReqs[0].Id)
	})

	t.Run("external Service without a floating IP gets one", func(t *testing.T) {
		client := newFakeLBClient()
		svc := newTestService()
		lbaas, _ := newTestLbaas(t, client, svc)
		lb := &lbv1.LoadBalancer{Id: lbID, Ip: "10.0.0.5"}

		_, err := lbaas.ensureFloatingIP(context.Background(), svc, lb, &serviceConfig{lbPublicNetworkID: testFloatingNetworkID, requestedFloatingIP: "203.0.113.77"})
		assert.ErrorContains(t, err, "being allocated")
		require.Len(t, client.allocateReqs, 1)
		assert.Equal(t, testFloatingNetworkID, client.allocateReqs[0].FipNetworkId)
		assert.Equal(t, "203.0.113.77", client.allocateReqs[0].Fip)
	})

	t.Run("external Service without a floating network uses the VIP", func(t *testing.T) {
		client := newFakeLBClient()
		svc := newTestService()
		lbaas, _ := newTestLbaas(t, client, svc)

		addr, err := lbaas.ensureFloatingIP(context.Background(), svc, &lbv1.LoadBalancer{Id: lbID, Ip: "10.0.0.5"}, &serviceConfig{})
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.5", addr)
		assert.Empty(t, client.allocateReqs)
	})
}

func TestGetLoadBalancer(t *testing.T) {
	client := newFakeLBClient()
	svc := newTestService()
	lbaas, kclient := newTestLbaas(t, client, svc)

	_, exists, err := lbaas.GetLoadBalancer(context.Background(), testLBClusterName, svc)
	require.NoError(t, err)
	assert.False(t, exists)

	_, err = lbaas.EnsureLoadBalancer(context.Background(), testLBClusterName, svc, newTestNodes("10.0.0.11"))
	require.NoError(t, err)
	stored, err := kclient.CoreV1().Services("default").Get(context.Background(), "web", v1.GetOptions{})
	require.NoError(t, err)

	lbStatus, exists, err := lbaas.GetLoadBalancer(context.Background(), testLBClusterName, stored)
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "203.0.113.10", lbStatus.Ingress[0].IP)

	lbStatus, exists, err = lbaas.GetLoadBalancer(context.Background(), testLBClusterName, newTestService())
	require.NoError(t, err)
	assert.True(t, exists, "found by name without the annotation")
	assert.Equal(t, "203.0.113.10", lbStatus.Ingress[0].IP)

	client.lbs["00000000-0000-4000-8000-000000000001"].State = lbv1.State_STATE_DELETING
	_, exists, err = lbaas.GetLoadBalancer(context.Background(), testLBClusterName, stored)
	require.NoError(t, err)
	assert.False(t, exists, "a load balancer being deleted does not exist")

	client.getErr = status.Error(codes.Unavailable, "down")
	_, _, err = lbaas.GetLoadBalancer(context.Background(), testLBClusterName, stored)
	assert.Error(t, err)
}

func TestEnsureLoadBalancerDeleted(t *testing.T) {
	const lbID = "00000000-0000-4000-8000-000000000001"
	spec := &lbv1.LoadBalancerSpec{TenantId: testTenantID}
	withID := func(svc *corev1.Service) *corev1.Service {
		svc.Annotations = map[string]string{ServiceAnnotationLoadBalancerID: lbID}
		return svc
	}

	t.Run("by ID", func(t *testing.T) {
		client := newFakeLBClient()
		client.add(&lbv1.LoadBalancer{Id: lbID, Name: testLBName, Spec: spec, State: lbv1.State_STATE_READY})
		svc := withID(newTestService())
		lbaas, _ := newTestLbaas(t, client, svc)

		require.NoError(t, lbaas.EnsureLoadBalancerDeleted(context.Background(), testLBClusterName, svc))
		require.Len(t, client.deleteReqs, 1)
		assert.Equal(t, lbID, client.deleteReqs[0].Id)
		assert.False(t, client.deleteReqs[0].PreserveFloatingIp)
		assert.Empty(t, client.lbs)
	})

	t.Run("by name when the annotation is missing", func(t *testing.T) {
		client := newFakeLBClient()
		client.add(&lbv1.LoadBalancer{Id: lbID, Name: testLBName, Spec: spec, State: lbv1.State_STATE_FAILED})
		svc := newTestService()
		lbaas, _ := newTestLbaas(t, client, svc)

		require.NoError(t, lbaas.EnsureLoadBalancerDeleted(context.Background(), testLBClusterName, svc))
		require.Len(t, client.deleteReqs, 1)
		assert.Equal(t, lbID, client.deleteReqs[0].Id)
	})

	t.Run("keep-floatingip", func(t *testing.T) {
		client := newFakeLBClient()
		client.add(&lbv1.LoadBalancer{Id: lbID, Name: testLBName, Spec: spec, State: lbv1.State_STATE_READY})
		svc := withID(newTestService())
		svc.Annotations[ServiceAnnotationLoadBalancerKeepFloatingIP] = "true"
		lbaas, _ := newTestLbaas(t, client, svc)

		require.NoError(t, lbaas.EnsureLoadBalancerDeleted(context.Background(), testLBClusterName, svc))
		assert.True(t, client.deleteReqs[0].PreserveFloatingIp)
	})

	t.Run("already gone", func(t *testing.T) {
		client := newFakeLBClient()
		svc := withID(newTestService())
		lbaas, _ := newTestLbaas(t, client, svc)

		require.NoError(t, lbaas.EnsureLoadBalancerDeleted(context.Background(), testLBClusterName, svc))
		assert.Empty(t, client.deleteReqs)
	})

	t.Run("never created", func(t *testing.T) {
		client := newFakeLBClient()
		svc := newTestService()
		lbaas, _ := newTestLbaas(t, client, svc)

		require.NoError(t, lbaas.EnsureLoadBalancerDeleted(context.Background(), testLBClusterName, svc))
		assert.Empty(t, client.deleteReqs)
	})

	t.Run("waits until the load balancer is gone", func(t *testing.T) {
		client := newFakeLBClient()
		client.purgeOnDelete = false
		client.add(&lbv1.LoadBalancer{Id: lbID, Name: testLBName, Spec: spec, State: lbv1.State_STATE_READY})
		svc := withID(newTestService())
		lbaas, _ := newTestLbaas(t, client, svc)

		err := lbaas.EnsureLoadBalancerDeleted(context.Background(), testLBClusterName, svc)
		assert.ErrorContains(t, err, "failed to delete within the allotted time")

		// A second call must not send another delete for a load balancer that is already on its way out.
		err = lbaas.EnsureLoadBalancerDeleted(context.Background(), testLBClusterName, svc)
		assert.Error(t, err)
		assert.Len(t, client.deleteReqs, 1)

		delete(client.lbs, lbID)
		require.NoError(t, lbaas.EnsureLoadBalancerDeleted(context.Background(), testLBClusterName, svc))
	})

	t.Run("delete fails", func(t *testing.T) {
		client := newFakeLBClient()
		client.deleteErr = status.Error(codes.Unavailable, "down")
		client.add(&lbv1.LoadBalancer{Id: lbID, Name: testLBName, Spec: spec, State: lbv1.State_STATE_READY})
		svc := withID(newTestService())
		lbaas, _ := newTestLbaas(t, client, svc)

		assert.Error(t, lbaas.EnsureLoadBalancerDeleted(context.Background(), testLBClusterName, svc),
			"an error keeps the cleanup finalizer on the Service")
	})

	t.Run("lookup fails", func(t *testing.T) {
		client := newFakeLBClient()
		client.listErr = status.Error(codes.Unavailable, "down")
		svc := newTestService()
		lbaas, _ := newTestLbaas(t, client, svc)

		assert.Error(t, lbaas.EnsureLoadBalancerDeleted(context.Background(), testLBClusterName, svc))
	})

	t.Run("a load balancer of another Service is not deleted", func(t *testing.T) {
		client := newFakeLBClient()
		client.add(&lbv1.LoadBalancer{Id: lbID, Name: "kube_service_kubernetes_default_other", Spec: spec, State: lbv1.State_STATE_READY})
		svc := withID(newTestService())
		lbaas, _ := newTestLbaas(t, client, svc)

		require.NoError(t, lbaas.EnsureLoadBalancerDeleted(context.Background(), testLBClusterName, svc))
		assert.Empty(t, client.deleteReqs)
		assert.Len(t, client.lbs, 1)
	})
}

func TestCreateLoadBalancerStatus(t *testing.T) {
	vip := corev1.LoadBalancerIPModeVIP

	t.Run("IP", func(t *testing.T) {
		lbaas := &LbaasV2{}
		got := lbaas.createLoadBalancerStatus(newTestService(), "203.0.113.10")
		assert.Equal(t, []corev1.LoadBalancerIngress{{IP: "203.0.113.10", IPMode: &vip}}, got.Ingress)
	})

	t.Run("hostname annotation", func(t *testing.T) {
		lbaas := &LbaasV2{}
		svc := newTestService()
		svc.Annotations = map[string]string{ServiceAnnotationLoadBalancerLoadbalancerHostname: "web.example.com"}
		got := lbaas.createLoadBalancerStatus(svc, "203.0.113.10")
		assert.Equal(t, []corev1.LoadBalancerIngress{{Hostname: "web.example.com"}}, got.Ingress)
	})

	t.Run("ingress hostname", func(t *testing.T) {
		lbaas := &LbaasV2{LoadBalancer{opts: LoadBalancerOpts{EnableIngressHostname: true, IngressHostnameSuffix: "nip.io"}}}
		got := lbaas.createLoadBalancerStatus(newTestService(), "203.0.113.10")
		assert.Equal(t, []corev1.LoadBalancerIngress{{Hostname: "203.0.113.10.nip.io"}}, got.Ingress)
	})
}

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**  *generated with [DocToc](https://github.com/thlorenz/doctoc)*

- [Exposing applications using services of LoadBalancer type](#exposing-applications-using-services-of-loadbalancer-type)
  - [Creating a Service of LoadBalancer type](#creating-a-service-of-loadbalancer-type)
  - [How a Service becomes a load balancer](#how-a-service-becomes-a-load-balancer)
  - [Internal and external load balancers](#internal-and-external-load-balancers)
  - [HTTP listeners](#http-listeners)
  - [Health monitors](#health-monitors)
  - [Service annotations](#service-annotations)
  - [Updates and deletion](#updates-and-deletion)
  - [Not supported](#not-supported)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Exposing applications using services of LoadBalancer type

This page shows how to create Services of LoadBalancer type in a Kubernetes cluster running on OpenStack. For an explanation of the Service concept, see [Services](https://kubernetes.io/docs/concepts/services-networking/service/).

openstack-cloud-controller-manager creates the load balancer of such a Service through the kontainercloud load balancer service (gRPC), not through Octavia. Its settings are described in the [Load Balancer section](./using-openstack-cloud-controller-manager.md#load-balancer) of the configuration.

**NOTE: for test/PoC with only 1 master node environment, you need remove the label `node.kubernetes.io/exclude-from-external-load-balancers` of the master node otherwise the loadbalancer will not be created. Refer to [here](https://kubernetes.io/docs/reference/labels-annotations-taints/#node-kubernetes-io-exclude-from-external-load-balancers) for further information.**

## Creating a Service of LoadBalancer type

Create an application of Deployment as the Service backend:

```shell
kubectl run echoserver --image=gcr.io/google-containers/echoserver:1.10 --port=8080
```

Expose it with a Service of type LoadBalancer:

```shell
cat <<EOF | kubectl apply -f -
---
kind: Service
apiVersion: v1
metadata:
  name: loadbalanced-service
spec:
  selector:
    run: echoserver
  type: LoadBalancer
  ports:
  - port: 80
    targetPort: 8080
    protocol: TCP
EOF
```

Check the state of the Service until `EXTERNAL-IP` is set:

```shell
$ kubectl get service loadbalanced-service
NAME                   TYPE           CLUSTER-IP     EXTERNAL-IP     PORT(S)        AGE
loadbalanced-service   LoadBalancer   10.254.28.183  203.0.113.10    80:30000/TCP   2m
```

## How a Service becomes a load balancer

| Service | Load balancer |
| --- | --- |
| The Service | One load balancer named `kube_service_<cluster-name>_<namespace>_<name>` |
| Each port | One listener on `port`, protocol `TCP` or `UDP` (`HTTP` with the `http-path` annotation). SCTP is not supported, and a port number cannot be used for both TCP and UDP. |
| Nodes | The backends of every listener: each node's internal address (external address as fallback) with the port's `nodePort`. `node-selector` limits the nodes. |
| `spec.loadBalancerIP` | The floating IP to request, in external mode |

openstack-cloud-controller-manager records these annotations on the Service; do not change them:

| Annotation | Content |
| --- | --- |
| `loadbalancer.openstack.org/load-balancer-id` | ID of the load balancer |
| `loadbalancer.openstack.org/load-balancer-address` | Address reported in the Service status |
| `loadbalancer.openstack.org/config-hash` | Fingerprint of the configuration last sent |
| `loadbalancer.openstack.org/create-attempt` | Counter that makes every creation attempt unique |

## Internal and external load balancers

The mode is chosen once for the whole cluster with `internal-lb` in the configuration (`loadBalancer.mode` in the Helm chart):

| Mode | Floating IP | The Service is reached at |
| --- | --- | --- |
| external (`internal-lb = false`) | always, from `floating-network-id` | the floating IP |
| internal (`internal-lb = true`) | never; an existing one is released | the load balancer's address on the cluster subnet |

The annotation `service.beta.kubernetes.io/openstack-internal-load-balancer` cannot change the mode. A Service carrying a value that differs from the mode gets a `LoadBalancerInternalAnnotationIgnored` warning event.

## HTTP listeners

The annotation `loadbalancer.openstack.org/http-path` turns every TCP port of the Service into an HTTP listener; UDP ports stay UDP.

```yaml
apiVersion: v1
kind: Service
metadata:
  name: web
  annotations:
    loadbalancer.openstack.org/http-path: "/api"
    loadbalancer.openstack.org/http-hostnames: "app.example.com"
    loadbalancer.openstack.org/health-monitor-http-path: "/healthz"
    loadbalancer.openstack.org/health-monitor-expected-codes: "200,204"
spec:
  type: LoadBalancer
  selector:
    app: web
  ports:
  - port: 80
    targetPort: 8080
```

- `http-path` is a path prefix the listener forwards; `/` forwards every request.
- `http-method` restricts the listener to one request method.
- `http-hostnames` restricts it to the listed `Host` headers. A leading `*.` wildcard is allowed, at most 16 names.
- Adding or removing `http-path` replaces the listener of each TCP port. Traffic on those ports is interrupted while the new listener is provisioned.

## Health monitors

TCP and HTTP listeners get a health monitor unless it is turned off with `create-monitor = false` or per Service with `loadbalancer.openstack.org/enable-health-monitor: "false"`. UDP listeners never get one: the load balancer checks backends with a TCP connect, which a UDP node port does not answer.

A TCP monitor checks that the node port accepts connections. An HTTP monitor requests `health-monitor-http-path` (default: the `http-path` value) with `health-monitor-http-method` (default `GET`) and expects one of `health-monitor-expected-codes` (default `200`).

For `externalTrafficPolicy: Local`, a node without a local pod refuses connections on the node port, so it is taken out of rotation by the same TCP check.

## Service annotations

| Annotation | Default | Effect |
| --- | --- | --- |
| `loadbalancer.openstack.org/subnet-id` | `subnet-id` | Subnet of the load balancer, used when it is created |
| `loadbalancer.openstack.org/network-id` | `network-id` | Network of the load balancer, used when it is created |
| `loadbalancer.openstack.org/floating-network-id` | `floating-network-id` | Network of the floating IP, external mode |
| `loadbalancer.openstack.org/keep-floatingip` | `false` | `true` keeps the floating IP in the project when the load balancer is deleted |
| `loadbalancer.openstack.org/connection-limit` | no limit | Maximum number of client connections |
| `loadbalancer.openstack.org/lb-method` | `lb-method` | Load balancing algorithm |
| `loadbalancer.openstack.org/node-selector` | `node-selector` | Comma-separated `key=value` labels the backend nodes must carry |
| `loadbalancer.openstack.org/enable-health-monitor` | `create-monitor` | Health monitor on or off |
| `loadbalancer.openstack.org/health-monitor-delay` | `monitor-delay` | Seconds between checks |
| `loadbalancer.openstack.org/health-monitor-timeout` | `monitor-timeout` | Seconds to wait for a check, less than the delay |
| `loadbalancer.openstack.org/health-monitor-max-retries` | `monitor-max-retries` | Successful checks to become healthy |
| `loadbalancer.openstack.org/health-monitor-max-retries-down` | `monitor-max-retries-down` | Failed checks to become unhealthy |
| `loadbalancer.openstack.org/http-path` | none | Turns the TCP ports into HTTP listeners, see above |
| `loadbalancer.openstack.org/http-method` | any | HTTP method the listeners accept |
| `loadbalancer.openstack.org/http-hostnames` | any | `Host` headers the listeners accept |
| `loadbalancer.openstack.org/health-monitor-http-path` | `http-path` | Path of the HTTP health check |
| `loadbalancer.openstack.org/health-monitor-http-method` | `GET` | Method of the HTTP health check |
| `loadbalancer.openstack.org/health-monitor-expected-codes` | `200` | Status codes of a healthy backend |
| `loadbalancer.openstack.org/hostname` | none | Hostname reported in the Service status instead of the IP |

## Updates and deletion

- A change of the Service's ports, annotations or nodes updates the existing load balancer. An update is only sent when the configuration changed, and listeners keep their identity, so adding a node does not rebuild them.
- No update is sent while the load balancer is still being provisioned; it is sent once the load balancer is ready. A failed load balancer is reported with a warning event and retried by the next update.
- The subnet and network of a load balancer cannot change after creation; changing them on the Service gives a `LoadBalancerNetworkChangeIgnored` warning event.
- Deleting the Service deletes the load balancer and waits until it is gone. Its floating IP is released unless `keep-floatingip` is `true`.
- Deleting the workload cluster does not delete its Services, so their load balancers stay. Delete the `LoadBalancer` Services before the cluster.

## Not supported

These features of the former Octavia implementation have no counterpart; their annotations are ignored with a `LoadBalancerAnnotationIgnored` warning event:

- `spec.loadBalancerSourceRanges`
- TLS termination (`default-tls-container-ref`)
- PROXY protocol and `X-Forwarded-For`
- Listener timeouts, flavors and availability zones
- Sharing one load balancer between Services, and the `loadbalancer.openstack.org/port-id` annotation
- Load balancer classes and floating subnets
- Tags on load balancers, listeners and pools
- IPv6 and dual-stack Services

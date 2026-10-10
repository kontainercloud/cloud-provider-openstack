# openstack-cloud-controller-manager

Deploys the OpenStack Cloud Controller Manager in a Cluster API management
cluster (for example a CAPO cluster). It manages one workload cluster from
there: install one release per workload cluster, into the namespace of its
`Cluster` object. The controller reaches the workload cluster through the
kubeconfig Secret Cluster API keeps for it, and OpenStack through `cloud.conf`.
Load balancers of `LoadBalancer` Services are created by the load balancer
service, not by Octavia.

## How To install

Write an `openstack-ccm.yaml` values file. These values have no usable default
and must be set:

| Value | Content |
| --- | --- |
| `cloudConfig.global.auth-url` | Keystone URL |
| `cloudConfig.global` credentials | `username` with `password`, or `application-credential-id` with `application-credential-secret` (`user-id`, `application-credential-name`, `token` and trust credentials work too) |
| `loadBalancer.rpcServerAddr` | Address of the load balancer service: `host:port` or `http://host:port`, for example `loadbalancer-api.lb-system.svc:8080` |
| `loadBalancer.networkID` | Network of the workload cluster's nodes, where the load balancers are created |
| `loadBalancer.subnetID` | Subnet of the workload cluster's nodes |
| `loadBalancer.floatingNetworkID` | External network floating IPs are allocated from; only for `loadBalancer.mode: external` (the default) |

Optional: `loadBalancer.apiKey` (when the load balancer service requires one),
`loadBalancer.tenantID` (defaults to the project of the credentials),
`loadBalancer.mode`, and `cloudConfig.global.region`. All IDs are UUIDs in lower
case. Cluster API reports the network and subnet of a CAPO cluster in the
status of its `OpenStackCluster`.

```yaml
cloudConfig:
  global:
    auth-url: https://keystone.example.com:5000/v3
    region: RegionOne
    application-credential-id: <id>
    application-credential-secret: <secret>

loadBalancer:
  mode: external
  rpcServerAddr: loadbalancer-api.lb-system.svc:8080
  networkID: 11111111-1111-4111-8111-111111111111
  subnetID: 22222222-2222-4222-8222-222222222222
  floatingNetworkID: 33333333-3333-4333-8333-333333333333
```

Then install a release named after the workload cluster, in the namespace of its `Cluster` object:

```sh
helm repo add cpo https://kubernetes.github.io/cloud-provider-openstack
helm repo update
helm install mycluster cpo/openstack-cloud-controller-manager \
  --namespace <cluster-namespace> --values openstack-ccm.yaml
```

Load balancer ports have no security groups and no QoS policy, and no VPC CIDR
is set; that is what the controller does when `security-group-ids`,
`qos-policy-id` and `vpc-cidr` are not set. Health checks are attached to TCP and
HTTP listeners by default; set `cloudConfig.loadBalancer.create-monitor: false`
to turn them off. Further `[LoadBalancer]` keys that the `loadBalancer` values
do not cover go under `cloudConfig.loadBalancer`. Service annotations,
including the HTTP listener annotations, are described in [Exposing applications using services of LoadBalancer type](../../docs/openstack-cloud-controller-manager/expose-applications-using-loadbalancer-type-service.md).

What the defaults give you:

| Item | Default | Value to change it |
| --- | --- | --- |
| Workload | a `Deployment` with one pod, named after the release | `replicaCount`, `fullnameOverride` |
| Managed cluster | the release name, used for `--cluster-name` and so for load balancer names | `cluster.name` |
| Kubeconfig | Secret `<cluster>-kubeconfig`, key `value`, created by Cluster API | `kubeconfig.secretName`, `kubeconfig.secretKey` |
| Cloud config | Secret `<cluster>-cloud-config`, created from `cloudConfig` and `loadBalancer` | `secret.name`, `secret.create` |
| Controllers | `cloud-node`, `cloud-node-lifecycle`, `service` | `enabledControllers` |
| Scheduling | any node, pod network, no priority class | `nodeSelector`, `tolerations`, `hostNetwork`, `priorityClassName` |

Things to keep in mind:

- `cluster.name` must be unique among the clusters that share an OpenStack project.
- Pods in the management cluster must reach Keystone and the other OpenStack
  endpoints, the load balancer service, and the workload cluster's API server.
- The workload cluster's kubelets must run with `cloud-provider: external`, and
  the workload cluster must not run its own copy of the controller.
- The kubeconfig is read at startup. Cluster API rotates the client
  certificate in `<cluster>-kubeconfig` before it expires; restart the
  controller after a rotation, for example with a reloader.
- Delete the workload cluster's `LoadBalancer` Services before deleting the
  cluster, while the controller still runs, so that their load balancers are
  removed. Uninstall the release after the cluster is gone.

## Validation

Helm checks the values when the chart is installed, upgraded, templated or linted,
and fails with a message that names the value to fix:

- `values.schema.json` checks types, allowed values and formats: `loadBalancer.mode`
  is `external` or `internal`, the IDs are UUIDs, `cluster.name` is a DNS name,
  `enabledControllers` lists known controllers, and so on. A value the chart
  does not have, such as a typo or an option of an older version, is rejected.
- `templates/validate.yaml` checks that the required values are set and fit
  together: the Keystone URL and credentials, `loadBalancer.rpcServerAddr` (not `https://`,
  the load balancer service only serves plaintext HTTP/2), the network and subnet,
  the floating network in `external` mode, and that `cloudConfig.loadBalancer`
  does not set a key that the `loadBalancer` values already set.

The cloud config is only checked when the chart writes it. With
`secret.create: false` or `cloudConfigContents` it comes from somewhere else and
is not checked. With `cloudConfig.loadBalancer.enabled: false` the load balancer
values are not required.

## Load balancer mode

`loadBalancer.mode` decides for every `LoadBalancer` Service of the workload cluster:

| Mode | Floating IP | Service is reached at | Required |
| --- | --- | --- | --- |
| `external` (default) | always allocated from `loadBalancer.floatingNetworkID` | the floating IP | `loadBalancer.floatingNetworkID` |
| `internal` | never; an existing one is released | the load balancer's address on the cluster subnet | nothing |

```yaml
# external
loadBalancer:
  mode: external
  floatingNetworkID: 33333333-3333-4333-8333-333333333333
```

```yaml
# internal
loadBalancer:
  mode: internal
```

The chart writes `internal-lb` and `floating-network-id` into the `[LoadBalancer]`
section of `cloud.conf` from these values. A Service cannot choose another mode:
the `service.beta.kubernetes.io/openstack-internal-load-balancer` annotation is
ignored with a warning event. When you bring your own Secret
(`secret.create: false`), set `internal-lb` and `floating-network-id` in it
yourself; the controller refuses to start in external mode without a floating
network.

## Using an external secret

In order to use an external secret for the OCCM:

```yaml
secret:
  enabled: true
  name: cloud-config
  create: false
```

Create the secret with:

```sh
kubectl create secret -n <cluster-namespace> generic cloud-config --from-file=./cloud.conf
```

## Unsupported configurations

- The chart does not support the mounting of custom `clouds.yaml` files. Therefore, the following config values in the `[Global]` section won’t have any effect:
  - `use-clouds`
  - `clouds-file`
  - `cloud`

# openstack-cloud-controller-manager

Deploys the OpenStack Cloud Controller Manager.

By default the controller runs in a Cluster API management ("infra") cluster
and manages one workload cluster from there: one release per workload cluster,
installed into the namespace of its `Cluster` object. It reaches the workload
cluster through the kubeconfig Secret Cluster API keeps for it, and OpenStack
through `cloud.conf`.

## How To install

You need to configure an `openstack-ccm.yaml` values file with at least:

- `cloudConfig.global.auth-url` with the Keystone URL
- Authentication
  - with password: `cloudConfig.global.username` and `cloudconfig.global.password`
  - with application credentials: (`cloudConfig.global.application-credential-id` or `cloudConfig.global.application-credential-name`) and `cloudConfig.global.application-credential-secret`
- Load balancing
  - `loadBalancer.mode`: `external` (default) or `internal`, see below
  - `loadBalancer.floatingNetworkID` with the ID of the external network, required in `external` mode
  - `cloudConfig.loadBalancer.rpc-server-addr` with the address of the load balancer service, for example `loadbalancer-api.lb-system.svc:8080`
  - `cloudConfig.loadBalancer.api-key` if the load balancer service requires one

Health checks are attached to TCP and HTTP listeners by default. Set `cloudConfig.loadBalancer.create-monitor: false` to turn them off. Service annotations, including the HTTP listener annotations, are described in [Exposing applications using services of LoadBalancer type](../../docs/openstack-cloud-controller-manager/expose-applications-using-loadbalancer-type-service.md).

Then install a release named after the workload cluster, in the namespace of its `Cluster` object:

```sh
helm repo add cpo https://kubernetes.github.io/cloud-provider-openstack
helm repo update
helm install mycluster cpo/openstack-cloud-controller-manager \
  --namespace <cluster-namespace> --values openstack-ccm.yaml
```

What the defaults give you:

| Item | Default | Value to change it |
| --- | --- | --- |
| Workload | a `Deployment` with one pod, named after the release | `kind`, `replicaCount`, `fullnameOverride` |
| Managed cluster | the release name, used for `--cluster-name` and so for load balancer names | `cluster.name` |
| Kubeconfig | Secret `<cluster>-kubeconfig`, key `value`, created by Cluster API | `kubeconfig.secretName`, `kubeconfig.secretKey` |
| Cloud config | Secret `<cluster>-cloud-config`, created from `cloudConfig` | `secret.name`, `secret.create` |
| RBAC | none: the controller's permissions in the workload cluster come from the kubeconfig | `rbac.create` |
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
section of `cloud.conf` from these values, and refuses to render when
`external` has no floating network. A Service cannot choose another mode: the
`service.beta.kubernetes.io/openstack-internal-load-balancer` annotation is
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

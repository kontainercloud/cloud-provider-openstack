# openstack-cloud-controller-manager

Deploys the OpenStack Cloud Controller Manager to your cluster.

Default configuration values are the same as the CCM itself.

## How To install

You need to configure an `openstack-ccm.yaml` values file with at least:

- `cloudConfig.global.auth-url` with the Keystone URL
- Authentication
  - with password: `cloudConfig.global.username` and `cloudconfig.global.password`
  - with application credentials: (`cloudConfig.global.application-credential-id` or `cloudConfig.global.application-credential-name`) and `cloudConfig.global.application-credential-secret`
- Load balancing
  - `cloudConfig.loadBalancer.rpc-server-addr` with the address of the load balancer service, for example `loadbalancer-api.lb-system.svc:8080`
  - `cloudConfig.loadBalancer.api-key` if the load balancer service requires one
  - `cloudConfig.loadBalancer.floating-network-id` if the project has more than one external network

Health checks are attached to TCP listeners by default. Set `cloudConfig.loadBalancer.create-monitor: false` to turn them off.

Then run:

```sh
helm repo add cpo https://kubernetes.github.io/cloud-provider-openstack
helm repo update
helm install openstack-ccm cpo/openstack-cloud-controller-manager --values openstack-ccm.yaml
```

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
kubectl create secret -n kube-system generic cloud-config --from-file=./cloud.conf
```

## Running in a management cluster

By default the chart runs the controller inside the cluster it manages: a
DaemonSet on the control plane nodes, using the in-cluster credentials. The
controller can also run in another cluster, for example the Cluster API
management cluster that created the workload cluster. It then reaches the
workload cluster through a kubeconfig and OpenStack through `cloud.conf`.

Install one release per workload cluster, into the namespace of its `Cluster`
object. `ci/infra-values.yaml` is a complete example; the values that matter:

| Value | Setting | Why |
| --- | --- | --- |
| `kind` | `Deployment` | No per-node pods in the management cluster. Leader election keeps one active pod, its lease lives in the workload cluster. |
| `kubeconfig.secretName` | `<cluster>-kubeconfig` | The Secret Cluster API keeps for the workload cluster. It is passed as `--kubeconfig`, `--authentication-kubeconfig` and `--authorization-kubeconfig`. |
| `rbac.create` | `false` | The chart's RBAC would land in the management cluster. Permissions in the workload cluster come from the kubeconfig. |
| `cluster.name` | the workload cluster's name | Load balancer names are derived from it and must not collide between clusters. |
| `nameOverride`, `serviceAccountName` | unique per release | Needed when several releases share a namespace. |
| `hostNetwork`, `dnsPolicy`, `nodeSelector`, `tolerations`, `priorityClassName`, `extraVolumes`, `extraVolumeMounts` | `false`, `ClusterFirst`, `null`, `[]`, `""`, `[]`, `[]` | The defaults target control plane nodes of the managed cluster. |
| `enabledControllers` | `cloud-node`, `cloud-node-lifecycle`, `service` | The route controller needs `router-id` and `--cluster-cidr`. |

```sh
kubectl -n <cluster-namespace> create secret generic mycluster-cloud-config --from-file=cloud.conf
helm install occm-mycluster cpo/openstack-cloud-controller-manager \
  -n <cluster-namespace> -f ci/infra-values.yaml
```

Things to keep in mind:

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

## Tolerations

To deploy OCCM to worker nodes only (e.g. when the controlplane is isolated), adjust the tolerations in the chart:

```yaml
tolerations:
  - key: node.cloudprovider.kubernetes.io/uninitialized
    value: "true"
    effect: NoSchedule
```

## Unsupported configurations

- The chart does not support the mounting of custom `clouds.yaml` files. Therefore, the following config values in the `[Global]` section won’t have any effect:
  - `use-clouds`
  - `clouds-file`
  - `cloud`

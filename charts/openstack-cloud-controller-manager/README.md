# openstack-cloud-controller-manager

Deploys the OpenStack Cloud Controller Manager.

By default the controller runs in a Cluster API management ("infra") cluster
and manages one workload cluster from there: one release per workload cluster,
installed into the namespace of its `Cluster` object. It reaches the workload
cluster through the kubeconfig Secret Cluster API keeps for it, and OpenStack
through `cloud.conf`. To run it inside the cluster it manages instead, see
[Running inside the managed cluster](#running-inside-the-managed-cluster).

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

## Running inside the managed cluster

To run the controller inside the cluster it manages, as a DaemonSet on the
control plane nodes with the in-cluster credentials, use these values:

```yaml
kind: DaemonSet
kubeconfig:
  enabled: false
rbac:
  create: true
cluster:
  name: kubernetes
secret:
  name: cloud-config
serviceAccountName: cloud-controller-manager
fullnameOverride: openstack-cloud-controller-manager
hostNetwork: true
dnsPolicy: ClusterFirstWithHostNet
nodeSelector:
  node-role.kubernetes.io/control-plane: ""
tolerations:
  - key: node.cloudprovider.kubernetes.io/uninitialized
    value: "true"
    effect: NoSchedule
  - key: node-role.kubernetes.io/control-plane
    effect: NoSchedule
priorityClassName: system-node-critical
extraVolumes:
  - name: flexvolume-dir
    hostPath:
      path: /usr/libexec/kubernetes/kubelet-plugins/volume/exec
  - name: k8s-certs
    hostPath:
      path: /etc/kubernetes/pki
extraVolumeMounts:
  - name: flexvolume-dir
    mountPath: /usr/libexec/kubernetes/kubelet-plugins/volume/exec
    readOnly: true
  - name: k8s-certs
    mountPath: /etc/kubernetes/pki
    readOnly: true
enabledControllers:
  - cloud-node
  - cloud-node-lifecycle
  - route
  - service
```

and install into `kube-system`:

```sh
helm install openstack-ccm cpo/openstack-cloud-controller-manager \
  --namespace kube-system --values openstack-ccm.yaml --values in-cluster.yaml
```

To deploy OCCM to worker nodes only in this mode (e.g. when the controlplane is
isolated), keep only the first toleration above.

## Unsupported configurations

- The chart does not support the mounting of custom `clouds.yaml` files. Therefore, the following config values in the `[Global]` section won’t have any effect:
  - `use-clouds`
  - `clouds-file`
  - `cloud`

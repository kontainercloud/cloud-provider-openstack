<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**  *generated with [DocToc](https://github.com/thlorenz/doctoc)*

- [Get started with external openstack-cloud-controller-manager in Kubernetes](#get-started-with-external-openstack-cloud-controller-manager-in-kubernetes)
  - [Deploy a Kubernetes cluster with openstack-cloud-controller-manager using kubeadm](#deploy-a-kubernetes-cluster-with-openstack-cloud-controller-manager-using-kubeadm)
    - [Prerequisites](#prerequisites)
    - [Steps](#steps)
  - [Migrating from in-tree openstack cloud provider to external openstack-cloud-controller-manager](#migrating-from-in-tree-openstack-cloud-provider-to-external-openstack-cloud-controller-manager)
  - [Config openstack-cloud-controller-manager](#config-openstack-cloud-controller-manager)
    - [Global](#global)
    - [Networking](#networking)
    - [Load Balancer](#load-balancer)
    - [Metadata](#metadata)
  - [Exposing applications using services of LoadBalancer type](#exposing-applications-using-services-of-loadbalancer-type)
  - [Metrics](#metrics)
  - [Limitation](#limitation)
    - [OpenStack availability zone must not contain blank](#openstack-availability-zone-must-not-contain-blank)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Get started with external openstack-cloud-controller-manager in Kubernetes

External cloud providers were introduced as an Alpha feature in Kubernetes release 1.6. openstack-cloud-controller-manager is the implementation of external cloud provider for OpenStack clusters. An external cloud provider is a kubernetes controller that runs cloud provider-specific loops required for the functioning of kubernetes. These loops were originally a part of the `kube-controller-manager`, but they were tightly coupling the `kube-controller-manager` to cloud-provider specific code. In order to free the kubernetes project of this dependency, the `cloud-controller-manager` was introduced.

`cloud-controller-manager` allows cloud vendors and kubernetes core to evolve independent of each other. In prior releases, the core Kubernetes code was dependent upon cloud provider-specific code for functionality. In future releases, code specific to cloud vendors should be maintained by the cloud vendor themselves, and linked to `cloud-controller-manager` while running Kubernetes.

For more information about cloud-controller-manager, please see:

- <https://github.com/kubernetes/enhancements/tree/master/keps/sig-cloud-provider/2392-cloud-controller-manager>
- <https://kubernetes.io/docs/tasks/administer-cluster/running-cloud-controller/#running-cloud-controller-manager>
- <https://kubernetes.io/docs/tasks/administer-cluster/developing-cloud-controller-manager/>

**NOTE: Now, the openstack-cloud-controller-manager implementation is based on OpenStack Octavia, Neutron-LBaaS has been removed in openstack-cloud-controller-manager since v1.26.0. So make sure to use Octavia if upgrade to the latest openstack-cloud-controller-manager docker image.**

## Deploy a Kubernetes cluster with openstack-cloud-controller-manager using kubeadm

The following guide has been tested to install Kubernetes v1.17 on Ubuntu 18.04.

### Prerequisites

- docker, kubeadm, kubelet and kubectl has been installed.

### Steps

- Create the kubeadm config file according to [`manifests/controller-manager/kubeadm.conf`](../../manifests/controller-manager/kubeadm.conf)

- Bootstrap the cluster, make sure to install the [CNI network plugin](https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/create-cluster-kubeadm/#pod-network) as well.

    ```
    kubeadm init --config kubeadm.conf
    ```

- Bootstrap worker nodes. You need to set `--cloud-provider=external` for kubelet service before running `kubeadm join`.

- Create a secret containing the cloud configuration. You can find an example config file in [`manifests/controller-manager/cloud-config`](../../manifests/controller-manager/cloud-config). If you have certs you need put the cert file into folder `/etc/ssl/certs/` and update `ca-file` in the configuration file, refer to `ca-file` option [here](./using-openstack-cloud-controller-manager.md#global) for further information. After that, Save the configuration to a file named *cloud.conf*, then:

    ```shell
    kubectl create secret -n kube-system generic cloud-config --from-file=cloud.conf
    ```

- Create RBAC resources and openstack-cloud-controller-manager daemonset.

    ```shell
    kubectl apply -f https://raw.githubusercontent.com/kubernetes/cloud-provider-openstack/master/manifests/controller-manager/cloud-controller-manager-roles.yaml
    kubectl apply -f https://raw.githubusercontent.com/kubernetes/cloud-provider-openstack/master/manifests/controller-manager/cloud-controller-manager-role-bindings.yaml
    kubectl apply -f https://raw.githubusercontent.com/kubernetes/cloud-provider-openstack/master/manifests/controller-manager/openstack-cloud-controller-manager-ds.yaml
    ```

- Waiting for all the pods in kube-system namespace up and running.

## Migrating from in-tree openstack cloud provider to external openstack-cloud-controller-manager

If you are already running a Kubernetes cluster (installed by kubeadm) but using in-tree openstack cloud provider, switching to openstack-cloud-controller-manager is easy by following the steps in the demo below.

[![asciicast](https://asciinema.org/a/303399.svg)](https://asciinema.org/a/303399?speed=2)

Also, checkout the guide on [Migrate to CCM](./migrate-to-ccm-with-csimigration.md)

## Config openstack-cloud-controller-manager

Implementation of openstack-cloud-controller-manager relies on several OpenStack services.

| Service                        | API Version(s) | Deprecated | Required |
|--------------------------------|----------------|------------|----------|
| Identity (Keystone)            | v3             | No         | Yes      |
| Compute (Nova)                 | v2             | No         | Yes      |
| Networking (Neutron)           | v2             | No         | Yes      |

Load balancers are not created through Octavia. They are managed by the
kontainercloud load balancer service (`loadbalancer.v1.LoadBalancerService`),
which openstack-cloud-controller-manager calls over gRPC.

NOTE:

* Block Storage is not needed for openstack-cloud-controller-manager in favor of [cinder-csi-plugin](../cinder-csi-plugin/using-cinder-csi-plugin.md).

### Global

The options in `Global` section are used for openstack-cloud-controller-manager authentication with OpenStack Keystone, they are similar to the global options when using `openstack` CLI, see more information in [openstack man page](https://docs.openstack.org/python-openstackclient/latest/cli/man/openstack.html).

* `auth-url`
  Required. Keystone service URL, e.g. http://128.110.154.166/identity
* `os-endpoint-type`
  Optional. Specify which type of endpoint to use from the service catalog.
  If not set, public endpoints are used.
* `ca-file`
  Optional. CA certificate bundle file for communication with Keystone service, this is required when using the https protocol in the Keystone service URL.
* `cert-file`
  Optional. Client certificate path used for the client TLS authentication.
* `key-file`
  Optional. Client private key path used for the client TLS authentication.
* `username`
  Keystone user name. If you are using [Keystone application credential](https://docs.openstack.org/keystone/latest/user/application_credentials.html), this option is not required.
* `password`
  Keystone user password. If you are using [Keystone application credential](https://docs.openstack.org/keystone/latest/user/application_credentials.html), this option is not required.
* `region`
  Required. Keystone region name.
* `domain-id`
  Keystone user domain ID. If you are using [Keystone application credential](https://docs.openstack.org/keystone/latest/user/application_credentials.html), this option is not required.
* `domain-name`
  Keystone user domain name, not required if `domain-id` is set.
* `tenant-id`
  Keystone project ID. When using Keystone V3 - which changed the identifier `tenant` to `project` - the `tenant-id` value is automatically mapped to the project construct in the API.

  `tenant-id` is not needed when using `trust-id` or [Keystone application credential](https://docs.openstack.org/keystone/latest/user/application_credentials.html)
* `tenant-name`
  Keystone project name, not required if `tenant-id` is set.
* `tenant-domain-id`
  Keystone project domain ID.
* `tenant-domain-name`
  Keystone project domain name.
* `user-domain-id`
  Keystone user domain ID.
* `user-domain-name`
  Keystone user domain name.
* `trust-id`
  Keystone trust ID. A trust represents a user's (the trustor) authorization to delegate roles to another user (the trustee), and optionally allow the trustee to impersonate the trustor. Available trusts are found under the `/v3/OS-TRUST/trusts` endpoint of the Keystone API.
* `trustee-id`
  Keystone trustee user ID.
* `trustee-password`
  Keystone trustee user password.
* `use-clouds`
  Set this option to `true` to get authorization credentials from a clouds.yaml file. Options explicitly set in this section are prioritized over values read from clouds.yaml, the file path can be set in `clouds-file` option. Otherwise, the following order is applied:
  1. A file path stored in the environment variable `OS_CLIENT_CONFIG_FILE`
  2. The directory `pkg/openstack`
  3. The directory `~/.config/openstack`
  4. The directory `/etc/openstack`
* `clouds-file`
  File path of a clouds.yaml file, used together with `use-clouds=true`.
* `cloud`
  Used to specify which named cloud in the clouds.yaml file that you want to use, used together with `use-clouds=true`.
* `application-credential-id`
  The ID of an application credential to authenticate with. An `application-credential-secret` has to be set along with this parameter.
* `application-credential-name`
  The name of an application credential to authenticate with. If `application-credential-id` is not set, the user name and domain need to be set.
* `application-credential-secret`
  The secret of an application credential to authenticate with.
* `tls-insecure`
  If set to `true`, then the server’s certificate will not be verified. Default is `false`.
* `token`
  Keystone token.

###  Networking

* `ipv6-support-disabled`
  Indicates whether or not IPv6 is supported. Default: false
* `public-network-name`
  The name of Neutron external network. openstack-cloud-controller-manager uses this option when getting the external IP of the Kubernetes node. Can be specified multiple times. Specified network names will be ORed. Default: ""
* `internal-network-name`
  The name of Neutron internal network. openstack-cloud-controller-manager uses this option when getting the internal IP of the Kubernetes node, this is useful if the node has multiple interfaces. Can be specified multiple times. Specified network names will be ORed. Default: ""
* `address-sort-order`
  This configuration key influences the way the provider reports the node addresses to the Kubernetes node resource. The default order depends on the hard-coded order the provider queries the addresses and what the cloud returns, which does not guarantee a specific order.

  To override this behavior it is possible to specify a comma separated list of CIDRs. Essentially, this will sort and group all addresses matching a CIDR in a prioritized manner, where the first item having a higher priority than the last. All non-matching addresses will remain in the same order they are already in.

  For example, this option can be useful when having multiple or dual-stack interfaces attached to a node and needing a user-controlled, deterministic way of sorting the addresses.
  Default: ""

### Route

* `router-id`
  Specifies the Neutron router ID to activate [route controller](https://kubernetes.io/docs/concepts/architecture/cloud-controller/#route-controller) to manage Kubernetes cluster routes.

  **NOTE: This require openstack-cloud-controller-manager's `--cluster-cidr` flag to be set.**

### Load Balancer

Load balancers for Services of type `LoadBalancer` are managed by the load
balancer service over gRPC. Every load balancer is created in one project, on
the cluster's subnet, with one listener per Service port whose backends are the
nodes' addresses and node ports.

#### Load balancer service

* `enabled`
  Whether or not to enable the LoadBalancer type of Services integration at all. Default: true
* `rpc-server-addr`
  Required. Address of the load balancer service. `host:port` or a URL such as `http://loadbalancer-api.lb-system.svc:8080`; the scheme and any path are removed, `http://` without a port means port 80. `https://` is rejected: the service only serves plaintext HTTP/2.
* `api-key`
  Sent as `Authorization: Bearer <api-key>` on every call. Empty means no credential is sent. Default: ""
* `rpc-timeout`
  Deadline of one call. Default: 30s
* `rpc-retry-max`
  Retries of a call that failed with a transient error. Default: 3
* `tenant-id`
  Project the load balancers are created in. Defaults to the project the `[Global]` credentials are scoped to.

#### Networks

* `subnet-id`
  Subnet of the load balancers. Can be overridden per Service with `loadbalancer.openstack.org/subnet-id`. Defaults to the subnet of the first node's address.
* `network-id`
  Network of `subnet-id`. Can be overridden per Service with `loadbalancer.openstack.org/network-id`. Defaults to the network of the subnet.
* `vpc-cidr`
  Address range of the VPC. Needed when nodes are in other subnets than the load balancer. Default: ""
* `security-group-ids`
  Comma-separated security groups applied to the load balancer's ports. Empty disables port security on them. Default: ""
* `qos-policy-id`
  QoS policy applied to the load balancer's ports. Empty means none. Default: ""

#### Load balancer mode

The mode applies to every Service of the cluster; a Service cannot choose another one.

* `internal-lb`
  `false` (external): every load balancer gets a floating IP and Services are reached at it; `floating-network-id` is required and openstack-cloud-controller-manager refuses to start without it. `true` (internal): no load balancer gets a floating IP, an existing one is released, and Services are reached at the load balancer's address on the cluster subnet. Default: false
* `floating-network-id`
  External network floating IPs are allocated from. Required unless `internal-lb` is true. Can be overridden per Service with `loadbalancer.openstack.org/floating-network-id`.

#### Listeners and health monitors

* `lb-method`
  Load balancing algorithm: `ROUND_ROBIN`, `LEAST_CONNECTIONS`, `SOURCE_IP` or `SOURCE_IP_PORT` (both map to consistent hashing). Default: ROUND_ROBIN
* `create-monitor`
  Whether a health monitor is attached to TCP and HTTP listeners. UDP listeners never get one. Default: true
* `monitor-delay`
  Time between two checks. Default: 5s
* `monitor-timeout`
  Time to wait for a check; must be less than `monitor-delay`. Default: 3s
* `monitor-max-retries`
  Successful checks before a backend counts as healthy. Default: 1
* `monitor-max-retries-down`
  Failed checks before a backend counts as unhealthy. Default: 3
* `node-selector`
  Comma-separated `key=value` labels; only matching nodes become backends. Default: ""

#### Other options

* `manage-security-groups`
  Create a security group per Service that opens its node ports to the load balancer's subnet, and attach it to the nodes' ports. Default: false
* `enable-ingress-hostname`
  Report `<address>.<ingress-hostname-suffix>` as hostname in the Service status instead of the IP. Default: false
* `ingress-hostname-suffix`
  Default: nip.io

The options of the former Octavia implementation (`lb-provider`, `lb-version`,
`member-subnet-id`, `floating-subnet*`, `cascade-delete`, `flavor-id`,
`availability-zone`, `max-shared-lb`, `container-store`,
`default-tls-container-ref`, `provider-requires-serial-api-calls` and the
`[LoadBalancerClass]` sections) have no effect; openstack-cloud-controller-manager
logs a warning at startup when one of them is set.

### Metadata

* `search-order`
  This configuration key influences the way that the provider retrieves metadata relating to the instance(s) in which it runs. The default value of `configDrive,metadataService` results in the provider retrieving metadata relating to the instance from the config drive first if available and then the metadata service. Alternative values are:
  * `configDrive` - Only retrieve instance metadata from the configuration drive.
  * `metadataService` - Only retrieve instance metadata from the metadata service.
  * `metadataService,configDrive` - Retrieve instance metadata from the metadata service first if available, then the configuration drive.

  Not all OpenStack clouds provide both configuration drive and metadata service though and only one or the other may be available which is why the default is to check both. Especially, the metadata on the config drive may grow stale over time, whereas the metadata service always provides the most up to date data.

### Multi region support (alpha)

* environment variable `OS_CCM_REGIONAL` is set to `true` - allow CCM to set ProviderID with region name `${ProviderName}://${REGION}/${instance-id}`. Default: false.

## Exposing applications using services of LoadBalancer type

Refer to [Exposing applications using services of LoadBalancer type](./expose-applications-using-loadbalancer-type-service.md)

## Metrics

Refer to [Metrics for openstack-cloud-controller-manager](../metrics.md)

## Limitation

### OpenStack availability zone must not contain blank

`topology.kubernetes.io/zone` is used to label node and its value comes from availability zone of the node, according to [label spec](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set) it does not support blank (' ') but OpenStack availability zone supports blank. So your OpenStack availability zone must not contain blank otherwise it will lead to node that belongs to this availability zone register failure, see [#1379](https://github.com/kubernetes/cloud-provider-openstack/issues/1379) for further information.

### OpenStack HostID label

`topology.openstack.org/host-id` is used to label node and its value comes from the host ID. The host ID represents the physical host your server runs on. This is a hashed value so will not actually look like a hostname, and is hashed with data from the project_id, so the same physical host as seen by two different project_ids, will be different. It is useful when within the same project you need to determine if two instances are on the same or different physical hosts for the purposes of availability or performance. Please be aware that real host ID can change in time, e.g. due to live migrations of the nodes, so take this label as only the "initial" host ID because it won't be reconciled.

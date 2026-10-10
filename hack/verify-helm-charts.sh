#!/usr/bin/env bash

# Copyright 2026 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${REPO_ROOT}"

if ! command -v helm &>/dev/null; then
  echo "ERROR: helm not found. Please install helm: https://helm.sh/docs/intro/install/" >&2
  exit 1
fi

echo "=== Linting charts ==="
# The OCCM chart has no usable defaults: it needs the Keystone credentials and
# the load balancer settings. Lint it in both load balancer modes.
occm_values=(
  --set-string cloudConfig.global.auth-url=http://keystone.example.com:5000/v3
  --set-string cloudConfig.global.application-credential-id=app-credential-id
  --set-string cloudConfig.global.application-credential-secret=app-credential-secret
  --set-string loadBalancer.rpcServerAddr=loadbalancer-api.lb-system.svc:8080
  --set-string loadBalancer.networkID=11111111-1111-4111-8111-111111111111
  --set-string loadBalancer.subnetID=22222222-2222-4222-8222-222222222222
)
helm lint $(ls -d charts/* | grep -v '^charts/openstack-cloud-controller-manager$')
helm lint charts/openstack-cloud-controller-manager "${occm_values[@]}" \
  --set-string loadBalancer.floatingNetworkID=33333333-3333-4333-8333-333333333333
helm lint charts/openstack-cloud-controller-manager "${occm_values[@]}" --set loadBalancer.mode=internal
for values_file in charts/*/ci/*.yaml; do
  chart_dir="$(dirname "$(dirname "${values_file}")")"
  helm lint "${chart_dir}" --values "${values_file}"
done

echo "=== Installing helm-unittest plugin ==="
if ! helm plugin list | grep -q unittest; then
  helm plugin install --verify=false \
    "https://github.com/helm-unittest/helm-unittest/releases/download/${HELM_UNITTEST_VERSION}/unittest-${HELM_UNITTEST_VERSION#v}.tgz"
fi

echo "=== Running helm unit tests ==="
helm unittest --strict --with-subchart=false \
  --file '../../tests/helm/openstack-cloud-controller-manager/*_test.yaml' \
  charts/openstack-cloud-controller-manager

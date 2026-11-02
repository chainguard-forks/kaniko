#!/usr/bin/env bash
# Copyright 2023 Google LLC
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

set -ex

export INSTALL_K3S_EXEC="--write-kubeconfig-mode=0644"
# Pin the k3s release. With INSTALL_K3S_VERSION set the installer downloads
# straight from GitHub releases and never asks update.k3s.io to resolve a
# channel; that lookup took every integration job down on 2026-11-02 when
# update.k3s.io was unreachable. Override INSTALL_K3S_VERSION to test another
# release.
export INSTALL_K3S_VERSION="${INSTALL_K3S_VERSION:-v1.36.5+k3s1}"
# Sometimes there is a residual kubeconfig, export and set this explicitly
mkdir -p "${HOME}/.kube"
export K3S_KUBECONFIG_OUTPUT="${HOME}/.kube/config"
export KUBECONFIG="${HOME}/.kube/config"
install_k3s() {
    curl -sfL -o /tmp/k3s-install.sh https://get.k3s.io && sh /tmp/k3s-install.sh
}
# One retry covers a transient download failure. If k3s still fails to
# start, the journal is the only place the cause is recorded, so dump it.
if ! install_k3s; then
    sleep 15
    install_k3s || { sudo journalctl -u k3s --no-pager | tail -n 50 || true; exit 1; }
fi
export SCRIPT_PATH="$(realpath $(dirname $0))"
timeout 5m bash -c 'until kubectl cluster-info 2>/dev/null | grep "CoreDNS" >/dev/null; do sleep 1; done'
# Install local registry and have it listen on localhost:5000
sudo cp "${SCRIPT_PATH}/local-registry-helm.yaml" /var/lib/rancher/k3s/server/manifests/
# Wait until install of the registry completes
timeout 5m bash -c 'until kubectl get -n kube-system pod 2>/dev/null | grep local-registry | grep Completed >/dev/null; do sleep 1; done'
# Wait until registry becomes available on localhost:5000
timeout 5m bash -c 'until nc -z localhost 5000; do sleep 1; done'

echo "K3s is running and registry is available on localhost:5000"

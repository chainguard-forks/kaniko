# Copyright 2018 Google, Inc. All rights reserved.
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

FROM golang:1.26@sha256:2005724102f45917a63e9d092fc0e4ea56ea575048ce147caad5f5f61502c365 AS builder
WORKDIR /src

# This arg is passed by docker buildx & contains the target CPU architecture (e.g., amd64, arm64, etc.)
ARG TARGETARCH
ARG TARGETOS

ENV GOARCH=$TARGETARCH
ENV GOOS=$TARGETOS

ENV CGO_ENABLED=0
ENV GOBIN=/usr/local/bin

# Add .docker config dir
RUN mkdir -p /kaniko/.docker

COPY . .

#  dependencies https://github.com/golang/go/issues/48332

# The versions of these tools and their transitive dependencies are controlled via go.mod.
# To update the version of any tool installed here, run
#
#   go get <tool>@<commit-sha-or-tag>
#   go mod vendor
#
# Then submit a PR with the changes to `go.mod`, `go.sum`, and `vendor`.

# Get GCR credential helper
RUN go install github.com/GoogleCloudPlatform/docker-credential-gcr/v2

# Get Amazon ECR credential helper
RUN go install github.com/awslabs/amazon-ecr-credential-helper/ecr-login/cli/docker-credential-ecr-login

# Get ACR docker env credential helper. This builds OUR patched copy, which
# lives in this repo as ordinary packages of the kaniko module under
# third_party/docker-credential-acr-env (derived from
# github.com/chrismellard/docker-credential-acr-env at 82a0ddb27589, with the
# GO-2026-6225 hostname check fixed; see PATCHES.md there). There is no go.mod
# replace and no upstream module involved. -mod=vendor stops Go resolving
# anything from the network. The binary name is unchanged.
RUN go install -mod=vendor ./third_party/docker-credential-acr-env

# Guard (GO-2026-6225): fail the build unless the helper's build info shows it
# is the in-tree package and that no github.com/chrismellard module was linked
# in. This reads the binary's build info rather than running it, so it works
# whatever the target arch (buildx emulation or a cross-compiled binary).
RUN info="$(go version -m /usr/local/bin/docker-credential-acr-env)" && \
    printf '%s\n' "${info}" | grep -Eq '^[[:space:]]*path[[:space:]]+github\.com/chainguard-dev/kaniko/third_party/docker-credential-acr-env$' && \
    ! printf '%s\n' "${info}" | grep -q 'github\.com/chrismellard' || { \
      echo "ERROR: docker-credential-acr-env is not the patched in-tree copy (third_party/docker-credential-acr-env)." >&2; \
      echo "It may ship the unpatched upstream helper (GO-2026-6225: ACR token leak to lookalike hosts)." >&2; \
      go version -m /usr/local/bin/docker-credential-acr-env >&2; \
      exit 1; }

# Functional check (GO-2026-6225), only when the binary can run on the build
# host: a lookalike ACR host must be answered with "credentials not found"
# before any token request. The lookalike uses the reserved .invalid TLD and
# AZURE_ENVIRONMENT is set to a non-existent cloud, so even an unpatched helper
# fails before any network or token request instead of leaking a token from
# the build machine.
RUN if [ "$(go env GOHOSTARCH)" = "$(go env GOARCH)" ] && [ "$(go env GOHOSTOS)" = "$(go env GOOS)" ]; then \
      out="$(printf 'evil.azurecr.io.example.invalid' | env -i HOME=/tmp AZURE_ENVIRONMENT=no-such-cloud /usr/local/bin/docker-credential-acr-env get 2>&1 || true)"; \
      echo "docker-credential-acr-env get evil.azurecr.io.example.invalid: ${out}"; \
      case "${out}" in \
        *"credentials not found"*) ;; \
        *) echo "ERROR: docker-credential-acr-env did not reject a lookalike ACR host (GO-2026-6225)." >&2; exit 1 ;; \
      esac; \
    else \
      echo "Skipping docker-credential-acr-env functional check: cross-compiled for $(go env GOOS)/$(go env GOARCH)."; \
    fi

RUN \
  --mount=type=cache,target=/root/.cache/go-build \
  --mount=type=cache,target=/go/pkg \
  make out/executor out/warmer

# Generate latest ca-certificates
FROM debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171 AS certs
RUN apt update && apt install -y ca-certificates

# use musl busybox since it's staticly compiled on all platforms
FROM busybox:musl@sha256:ea2b9914a16a4ac1981994af97b318f7c7d4db76b580c56177f08bf76f4a0be8 AS busybox

FROM scratch AS kaniko-base-slim

# Create kaniko directory with world write permission to allow non root run
RUN --mount=from=busybox,dst=/usr/ ["busybox", "sh", "-c", "mkdir -p /kaniko && chmod 777 /kaniko"]

COPY --from=certs /etc/ssl/certs/ca-certificates.crt /kaniko/ssl/certs/
COPY files/nsswitch.conf /etc/nsswitch.conf
ENV HOME=/root
ENV USER=root
ENV PATH=/usr/local/bin:/kaniko
ENV SSL_CERT_DIR=/kaniko/ssl/certs

FROM kaniko-base-slim AS kaniko-base

COPY --from=builder --chown=0:0 /usr/local/bin/docker-credential-gcr /kaniko/docker-credential-gcr
COPY --from=builder --chown=0:0 /usr/local/bin/docker-credential-ecr-login /kaniko/docker-credential-ecr-login
COPY --from=builder --chown=0:0 /usr/local/bin/docker-credential-acr-env /kaniko/docker-credential-acr-env

COPY --from=builder /kaniko/.docker /kaniko/.docker

ENV DOCKER_CONFIG=/kaniko/.docker/
ENV DOCKER_CREDENTIAL_GCR_CONFIG=/kaniko/.config/gcloud/docker_credential_gcr_config.json
WORKDIR /workspace

### FINAL STAGES ###

FROM kaniko-base AS kaniko-warmer

COPY --from=builder /src/out/warmer /kaniko/warmer

ENTRYPOINT ["/kaniko/warmer"]

FROM kaniko-base AS kaniko-executor

COPY --from=builder /src/out/executor /kaniko/executor

ENTRYPOINT ["/kaniko/executor"]

FROM kaniko-executor AS kaniko-debug

ENV PATH=/usr/local/bin:/kaniko:/busybox

COPY --from=builder /src/out/warmer /kaniko/warmer

COPY --from=busybox /bin /busybox
# Declare /busybox as a volume to get it automatically in the path to ignore
VOLUME /busybox

RUN ["/busybox/mkdir", "-p", "/bin"]
RUN ["/busybox/ln", "-s", "/busybox/sh", "/bin/sh"]

FROM kaniko-base-slim AS kaniko-slim

COPY --from=builder /src/out/executor /kaniko/executor

ENTRYPOINT ["/kaniko/executor"]

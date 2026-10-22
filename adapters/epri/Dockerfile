# SPDX-License-Identifier: MIT
# ieee20305-interop adapter for EPRI IEEE 2030.5 Client (C, BSD-3-Clause).
#
# Build from the repository root:
#   docker build -f adapters/epri/Dockerfile -t ieee20305-interop-epri:dev .
#
# The EPRI IEEE 2030.5 Client is a C implementation of an IEEE 2030.5 client
# (BSD-3-Clause). It communicates over HTTPS with mTLS using device
# certificates (LFDI/SFDI). The adapter wraps it in the ieee20305-interop
# container contract. Pins are mirrored in versions.yaml.
#
# STATUS: skeleton — adapter code is not yet written.

# Where the base image comes from. It is pinned by digest, so a mirror of
# Docker Hub serves exactly the same image; CI uses one to stay clear of
# Docker Hub's pull rate limit (see the Makefile).
ARG BASE_REGISTRY=docker.io/library
# debian:trixie-slim, multi-arch index (linux/amd64 and linux/arm64).
ARG DEBIAN_DIGEST=sha256:TODO_PIN_DEBIAN_TRIXIE_SLIM_DIGEST

# The upstream release pin and its commit.
ARG UPSTREAM_VERSION=main
ARG UPSTREAM_SHA=TODO

# ---------------------------------------------------------------------------
# Stage 1: build the EPRI IEEE 2030.5 Client and the adapter
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST} AS build

ARG UPSTREAM_VERSION
ARG UPSTREAM_SHA

RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential cmake git ca-certificates \
        libssl-dev libxml2-dev libcurl4-openssl-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Clone the EPRI IEEE 2030.5 Client at the pinned version.
RUN git clone --depth 1 --branch "${UPSTREAM_VERSION}" \
        https://github.com/epri-dev/IEEE-2030.5-Client.git upstream \
    && cd upstream \
    && if [ "${UPSTREAM_SHA}" != "TODO" ]; then \
           git fetch --depth 1 origin "${UPSTREAM_SHA}" && git checkout "${UPSTREAM_SHA}"; \
       fi

# Build the upstream client library.
# TODO: adjust build commands once the upstream build system is confirmed.
RUN cd upstream \
    && mkdir -p build && cd build \
    && cmake .. -DCMAKE_BUILD_TYPE=Release \
    && make -j"$(nproc)"

# TODO: build the adapter entrypoint that wraps the upstream client in the
# ieee20305-interop container contract (commands: server, client, call,
# print-capabilities, print-fixture, version).

# ---------------------------------------------------------------------------
# Stage 2: runtime image
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST}

ARG ADAPTER_VERSION=dev

RUN apt-get update && apt-get install -y --no-install-recommends \
        libssl3t64 libxml2 libcurl4t64 ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Non-root user.
RUN groupadd -g 10001 interop && useradd -u 10001 -g interop -s /bin/false interop

# Readiness and control paths.
RUN mkdir -p /run/ieee20305-interop && chown 10001:10001 /run/ieee20305-interop

# Fixtures baked into the image.
COPY fixtures/ /fixtures/

# Licences.
RUN mkdir -p /licenses/ieee20305-interop
COPY LICENSE /licenses/ieee20305-interop/LICENSE
# TODO: COPY THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md

# TODO: copy the built adapter binary and upstream libraries.
# COPY --from=build /src/upstream/build/... /usr/local/lib/
# COPY --from=build /src/adapter/ieee20305-interop /usr/local/bin/ieee20305-interop

# OCI labels.
LABEL org.opencontainers.image.source="https://github.com/otfabric/ieee20305-interop" \
      org.opencontainers.image.title="ieee20305-interop-epri" \
      org.opencontainers.image.description="EPRI IEEE 2030.5 Client adapter for ieee20305-interop" \
      org.opencontainers.image.licenses="MIT AND BSD-3-Clause" \
      org.opencontainers.image.version="${ADAPTER_VERSION}"

EXPOSE 443

HEALTHCHECK --interval=2s --timeout=2s --retries=15 --start-period=20s \
    CMD ["test", "-f", "/run/ieee20305-interop/ready"]

USER 10001

ENTRYPOINT ["ieee20305-interop"]
CMD ["client", "register"]

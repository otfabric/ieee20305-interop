# SPDX-License-Identifier: MIT
# ieee20305-interop adapter for the EPRI IEEE 2030.5 client (C,
# BSD-3-Clause). See README.md.
#
# Build from the repository root:
#   docker build -f adapters/epri/Dockerfile -t ghcr.io/otfabric/ieee20305-interop-epri:dev .
#
# Pins are mirrored in versions.yaml (make versions checks them).

# Where the base image comes from. It is pinned by digest, so a mirror of
# Docker Hub serves exactly the same image.
ARG BASE_REGISTRY=docker.io/library
# debian:trixie-slim, multi-arch index (linux/amd64 and linux/arm64).
ARG DEBIAN_DIGEST=sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad

FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST} AS build

# The upstream commit. EPRI tags v0.2.11 but publishes no release; the
# commit is the pin.
ARG UPSTREAM_VERSION=v0.2.11
ARG UPSTREAM_SHA=505ee47d9c3ed43ce63087198d09f77338da627d
# Debian trixie packages of the build.
ARG GCC_VERSION=4:14.2.0-1
ARG OPENSSL_VERSION=3.5.7-1~deb13u3

RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates patch \
        "gcc=${GCC_VERSION}" libc6-dev "libssl-dev=${OPENSSL_VERSION}" "openssl=${OPENSSL_VERSION}" \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src

# The upstream at its pinned commit.
RUN set -eu; \
    echo "${UPSTREAM_SHA}" | grep -Eq '^[0-9a-f]{40}$'; \
    git init --quiet epri; \
    git -C epri fetch --quiet --depth 1 https://github.com/epri-dev/IEEE-2030.5-Client.git "${UPSTREAM_SHA}"; \
    git -C epri checkout --quiet FETCH_HEAD; \
    test "$(git -C epri rev-parse HEAD)" = "${UPSTREAM_SHA}"

# Built with its own build script. targets.sh refuses any OpenSSL but 1.1.0;
# the build accepts the OpenSSL 3 of Debian trixie instead. One patch, see
# patches/.
COPY adapters/epri/patches/ /src/patches/
RUN set -eu; \
    cd epri; \
    grep -q '=~ "OpenSSL 1.1.0"' targets.sh; \
    sed -i 's/=~ "OpenSSL 1.1.0"/=~ "OpenSSL "/' targets.sh; \
    for p in /src/patches/*.patch; do patch -p1 --forward < "${p}"; done; \
    bash build.sh > /src/build.log 2>&1 || { cat /src/build.log; exit 1; }; \
    test -x build/client_test; \
    mkdir -p /out/epri /out/licenses/github.com/epri-dev/IEEE-2030.5-Client; \
    cp build/client_test /out/epri/; \
    cp -r settings /out/epri/settings; \
    cp LICENSE /out/licenses/github.com/epri-dev/IEEE-2030.5-Client/

FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST} AS runtime

ARG ADAPTER_VERSION=dev
ARG UPSTREAM_VERSION=v0.2.11
ARG UPSTREAM_SHA=505ee47d9c3ed43ce63087198d09f77338da627d
ARG OPENSSL_VERSION=3.5.7-1~deb13u3
ARG PYTHON3_VERSION=3.13.5-1

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        "libssl3t64=${OPENSSL_VERSION}" "python3=${PYTHON3_VERSION}" \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin interop \
    && mkdir -p /run/ieee20305-interop \
    && chown interop /run/ieee20305-interop

COPY --from=build /out/epri /opt/epri
COPY --from=build /out/licenses /licenses
COPY LICENSE /licenses/ieee20305-interop/LICENSE
COPY adapters/epri/THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md
COPY adapters/epri/openssl.cnf /opt/epri/openssl.cnf
COPY adapters/epri/adapter/ieee20305_interop.py /opt/ieee20305-interop/ieee20305_interop.py
COPY fixtures/ /fixtures/
RUN printf '#!/bin/sh\nexec /usr/bin/python3 -I /opt/ieee20305-interop/ieee20305_interop.py "$@"\n' \
        > /usr/local/bin/ieee20305-interop \
    && chmod 0755 /usr/local/bin/ieee20305-interop

ENV IEEE20305_INTEROP_ADAPTER_VERSION=${ADAPTER_VERSION} \
    IEEE20305_INTEROP_UPSTREAM_VERSION=${UPSTREAM_VERSION} \
    IEEE20305_INTEROP_UPSTREAM_REVISION=${UPSTREAM_SHA} \
    PYTHONDONTWRITEBYTECODE=1

LABEL org.opencontainers.image.source="https://github.com/otfabric/ieee20305-interop" \
      org.opencontainers.image.title="ieee20305-interop-epri" \
      org.opencontainers.image.description="EPRI IEEE 2030.5 client adapter for ieee20305-interop" \
      org.opencontainers.image.licenses="MIT AND BSD-3-Clause" \
      org.opencontainers.image.version="${ADAPTER_VERSION}"

HEALTHCHECK --interval=2s --timeout=2s --retries=15 --start-period=20s \
    CMD ["test", "-f", "/run/ieee20305-interop/ready"]

USER 10001

ENTRYPOINT ["ieee20305-interop"]
CMD ["server"]

# SPDX-License-Identifier: MIT
# ieee20305-interop adapter for ANU Envoy (Python/FastAPI, MIT).
#
# Build from the repository root:
#   docker build -f adapters/envoy/Dockerfile -t ieee20305-interop-envoy:dev .
#
# Envoy is an IEEE 2030.5 server with CSIP-AUS support, built on
# Python/FastAPI (MIT). It implements the server role of the IEEE 2030.5
# protocol over HTTPS with mTLS. The adapter wraps it in the
# ieee20305-interop container contract. Pins are mirrored in versions.yaml.
#
# STATUS: skeleton — adapter code is not yet written.

ARG BASE_REGISTRY=docker.io/library
# python:3.12-slim-trixie, multi-arch index (linux/amd64 and linux/arm64).
ARG PYTHON_DIGEST=sha256:TODO_PIN_PYTHON_SLIM_TRIXIE_DIGEST

ARG UPSTREAM_VERSION=main
ARG UPSTREAM_SHA=TODO

# ---------------------------------------------------------------------------
# Stage 1: install Envoy and its dependencies, build the adapter
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/python:3.12-slim-trixie@${PYTHON_DIGEST} AS build

ARG UPSTREAM_VERSION
ARG UPSTREAM_SHA

RUN apt-get update && apt-get install -y --no-install-recommends \
        git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Clone ANU Envoy at the pinned version.
RUN git clone --depth 1 --branch "${UPSTREAM_VERSION}" \
        https://github.com/bsgip/envoy.git upstream \
    && cd upstream \
    && if [ "${UPSTREAM_SHA}" != "TODO" ]; then \
           git fetch --depth 1 origin "${UPSTREAM_SHA}" && git checkout "${UPSTREAM_SHA}"; \
       fi

# Install Envoy and its dependencies.
# TODO: adjust once the package structure is confirmed; may need poetry or
# a requirements.txt export.
RUN pip install --no-cache-dir --prefix=/install ./upstream

# TODO: build/install the adapter entrypoint that wraps Envoy in the
# ieee20305-interop container contract.

# ---------------------------------------------------------------------------
# Stage 2: runtime image
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/python:3.12-slim-trixie@${PYTHON_DIGEST}

ARG ADAPTER_VERSION=dev

# Non-root user.
RUN groupadd -g 10001 interop && useradd -u 10001 -g interop -s /bin/false interop

# Readiness and control paths.
RUN mkdir -p /run/ieee20305-interop && chown 10001:10001 /run/ieee20305-interop

# Python packages from the build stage.
COPY --from=build /install /usr/local

# Fixtures baked into the image.
COPY fixtures/ /fixtures/

# Licences.
RUN mkdir -p /licenses/ieee20305-interop
COPY LICENSE /licenses/ieee20305-interop/LICENSE
# TODO: COPY THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md

# TODO: copy the adapter entrypoint.
# COPY adapters/envoy/ieee20305_interop/ /usr/local/lib/python3.12/site-packages/ieee20305_interop/
# COPY adapters/envoy/entrypoint.py /usr/local/bin/ieee20305-interop

# OCI labels.
LABEL org.opencontainers.image.source="https://github.com/otfabric/ieee20305-interop" \
      org.opencontainers.image.title="ieee20305-interop-envoy" \
      org.opencontainers.image.description="ANU Envoy IEEE 2030.5 server adapter for ieee20305-interop (CSIP-AUS)" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${ADAPTER_VERSION}"

EXPOSE 443

HEALTHCHECK --interval=2s --timeout=2s --retries=15 --start-period=20s \
    CMD ["test", "-f", "/run/ieee20305-interop/ready"]

USER 10001

ENTRYPOINT ["ieee20305-interop"]
CMD ["server"]

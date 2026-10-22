# SPDX-License-Identifier: MIT
# ieee20305-interop adapter for DER Security py20305 / Project Satori
# (Python, Apache-2.0).
#
# Build from the repository root:
#   docker build -f adapters/py20305/Dockerfile -t ieee20305-interop-py20305:dev .
#
# py20305 is a Python IEEE 2030.5 client from the DER Security / Project
# Satori effort (Apache-2.0). It communicates over HTTPS with mTLS. The
# adapter wraps it in the ieee20305-interop container contract.
# Pins are mirrored in versions.yaml.
#
# STATUS: skeleton — adapter code is not yet written.

ARG BASE_REGISTRY=docker.io/library
# python:3.12-slim-trixie, multi-arch index (linux/amd64 and linux/arm64).
ARG PYTHON_DIGEST=sha256:TODO_PIN_PYTHON_SLIM_TRIXIE_DIGEST

ARG UPSTREAM_VERSION=main
ARG UPSTREAM_SHA=TODO

# ---------------------------------------------------------------------------
# Stage 1: install py20305 and its dependencies, build the adapter
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/python:3.12-slim-trixie@${PYTHON_DIGEST} AS build

ARG UPSTREAM_VERSION
ARG UPSTREAM_SHA

RUN apt-get update && apt-get install -y --no-install-recommends \
        git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Clone py20305 at the pinned version.
RUN git clone --depth 1 --branch "${UPSTREAM_VERSION}" \
        https://github.com/DERSecurity/py20305.git upstream \
    && cd upstream \
    && if [ "${UPSTREAM_SHA}" != "TODO" ]; then \
           git fetch --depth 1 origin "${UPSTREAM_SHA}" && git checkout "${UPSTREAM_SHA}"; \
       fi

# Install py20305 and its dependencies.
# TODO: adjust once the package structure is confirmed.
RUN pip install --no-cache-dir --prefix=/install ./upstream

# TODO: build/install the adapter entrypoint that wraps py20305 in the
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
# COPY adapters/py20305/ieee20305_interop/ /usr/local/lib/python3.12/site-packages/ieee20305_interop/
# COPY adapters/py20305/entrypoint.py /usr/local/bin/ieee20305-interop

# OCI labels.
LABEL org.opencontainers.image.source="https://github.com/otfabric/ieee20305-interop" \
      org.opencontainers.image.title="ieee20305-interop-py20305" \
      org.opencontainers.image.description="DER Security py20305 IEEE 2030.5 client adapter for ieee20305-interop" \
      org.opencontainers.image.licenses="MIT AND Apache-2.0" \
      org.opencontainers.image.version="${ADAPTER_VERSION}"

EXPOSE 443

HEALTHCHECK --interval=2s --timeout=2s --retries=15 --start-period=20s \
    CMD ["test", "-f", "/run/ieee20305-interop/ready"]

USER 10001

ENTRYPOINT ["ieee20305-interop"]
CMD ["client", "register"]

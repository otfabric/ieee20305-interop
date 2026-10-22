# SPDX-License-Identifier: MIT
# ieee20305-interop adapter for py20305, the DER Security IEEE 2030.5 client
# (Python, Apache-2.0). See README.md.
#
# Build from the repository root:
#   docker build -f adapters/py20305/Dockerfile -t ghcr.io/otfabric/ieee20305-interop-py20305:dev .
#
# Pins are mirrored in versions.yaml (make versions checks them).

# Where the base image comes from. It is pinned by digest, so a mirror of
# Docker Hub serves exactly the same image.
ARG BASE_REGISTRY=docker.io/library
# python:3.13-slim-trixie, multi-arch index (linux/amd64 and linux/arm64).
ARG PYTHON_DIGEST=sha256:57b29a7c050c4e679e4bea572e14b3965cf4b5839de2da49d55e49ffc4024f39

FROM ${BASE_REGISTRY}/python:3.13-slim-trixie@${PYTHON_DIGEST} AS build

# The upstream release and its commit.
ARG UPSTREAM_VERSION=v0.16.0
ARG UPSTREAM_SHA=79844938eea3d05f5432cdc5f2d07a103e87912b

ENV PIP_NO_CACHE_DIR=1 PIP_DISABLE_PIP_VERSION_CHECK=1 PIP_ROOT_USER_ACTION=ignore
RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src

# The upstream at its pinned commit. A tag that moved fails the build.
RUN set -eu; \
    echo "${UPSTREAM_SHA}" | grep -Eq '^[0-9a-f]{40}$'; \
    git clone --quiet --depth 1 --branch "${UPSTREAM_VERSION}" https://github.com/DERSecurity/py20305.git py20305; \
    got="$(git -C py20305 rev-parse HEAD)"; \
    if [ "${got}" != "${UPSTREAM_SHA}" ]; then echo "py20305 ${UPSTREAM_VERSION} is commit ${got}, not ${UPSTREAM_SHA}" >&2; exit 1; fi

# Every dependency from the hashed lock, into a virtual environment; then
# py20305 itself, built from the pinned source with the locked build backend
# and no further downloads.
COPY adapters/py20305/requirements.txt /src/requirements.txt
RUN set -eu; \
    python -m venv /opt/venv; \
    /opt/venv/bin/pip install --require-hashes --only-binary=:all: -r requirements.txt; \
    /opt/venv/bin/pip install --no-deps --no-build-isolation --no-index ./py20305; \
    /opt/venv/bin/pip check; \
    /opt/venv/bin/python -c "import py20305.client.csip_client"

# Licences of py20305 and of everything installed beside it.
COPY adapters/py20305/collect_licenses.py /src/collect_licenses.py
RUN set -eu; \
    /opt/venv/bin/python collect_licenses.py /out/licenses; \
    mkdir -p /out/licenses/github.com/DERSecurity/py20305; \
    cp py20305/LICENSE py20305/NOTICE /out/licenses/github.com/DERSecurity/py20305/

FROM ${BASE_REGISTRY}/python:3.13-slim-trixie@${PYTHON_DIGEST} AS runtime

ARG ADAPTER_VERSION=dev
ARG UPSTREAM_VERSION=v0.16.0
ARG UPSTREAM_SHA=79844938eea3d05f5432cdc5f2d07a103e87912b

RUN useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin interop \
    && mkdir -p /run/ieee20305-interop \
    && chown interop /run/ieee20305-interop

COPY --from=build /opt/venv /opt/venv
COPY --from=build /out/licenses /licenses
COPY LICENSE /licenses/ieee20305-interop/LICENSE
COPY adapters/py20305/THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md
COPY adapters/py20305/adapter/ieee20305_interop.py /opt/ieee20305-interop/ieee20305_interop.py
COPY fixtures/ /fixtures/
RUN printf '#!/bin/sh\nexec /opt/venv/bin/python -I /opt/ieee20305-interop/ieee20305_interop.py "$@"\n' \
        > /usr/local/bin/ieee20305-interop \
    && chmod 0755 /usr/local/bin/ieee20305-interop

ENV IEEE20305_INTEROP_ADAPTER_VERSION=${ADAPTER_VERSION} \
    IEEE20305_INTEROP_UPSTREAM_VERSION=${UPSTREAM_VERSION} \
    IEEE20305_INTEROP_UPSTREAM_REVISION=${UPSTREAM_SHA} \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

LABEL org.opencontainers.image.source="https://github.com/otfabric/ieee20305-interop" \
      org.opencontainers.image.title="ieee20305-interop-py20305" \
      org.opencontainers.image.description="DER Security py20305 IEEE 2030.5 client adapter for ieee20305-interop" \
      org.opencontainers.image.licenses="MIT AND Apache-2.0" \
      org.opencontainers.image.version="${ADAPTER_VERSION}"

HEALTHCHECK --interval=2s --timeout=2s --retries=15 --start-period=20s \
    CMD ["test", "-f", "/run/ieee20305-interop/ready"]

USER 10001

ENTRYPOINT ["ieee20305-interop"]
CMD ["server"]

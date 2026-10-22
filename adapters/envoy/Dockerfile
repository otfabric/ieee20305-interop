# SPDX-License-Identifier: MIT
# ieee20305-interop adapter for Envoy, the ANU Battery Storage and Grid
# Integration Program's IEEE 2030.5 / CSIP-AUS server (Python, MIT). See
# README.md.
#
# Build from the repository root:
#   docker build -f adapters/envoy/Dockerfile -t ghcr.io/otfabric/ieee20305-interop-envoy:dev .
#
# Pins are mirrored in versions.yaml (make versions checks them).

# Where the base image comes from. It is pinned by digest, so a mirror of
# Docker Hub serves exactly the same image.
ARG BASE_REGISTRY=docker.io/library
# python:3.13-slim-trixie, multi-arch index (linux/amd64 and linux/arm64).
ARG PYTHON_DIGEST=sha256:57b29a7c050c4e679e4bea572e14b3965cf4b5839de2da49d55e49ffc4024f39

FROM ${BASE_REGISTRY}/python:3.13-slim-trixie@${PYTHON_DIGEST} AS build

# The upstream release and its commit.
ARG UPSTREAM_VERSION=v2.5.0
ARG UPSTREAM_SHA=9d00340b5592f1fdf09dc74bd743fc251da0bfb5

ENV PIP_NO_CACHE_DIR=1 PIP_DISABLE_PIP_VERSION_CHECK=1 PIP_ROOT_USER_ACTION=ignore
RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src

# The upstream at its pinned commit. A tag that moved fails the build.
RUN set -eu; \
    echo "${UPSTREAM_SHA}" | grep -Eq '^[0-9a-f]{40}$'; \
    git clone --quiet --depth 1 --branch "${UPSTREAM_VERSION}" https://github.com/bsgip/envoy.git envoy; \
    got="$(git -C envoy rev-parse HEAD)"; \
    if [ "${got}" != "${UPSTREAM_SHA}" ]; then echo "envoy ${UPSTREAM_VERSION} is commit ${got}, not ${UPSTREAM_SHA}" >&2; exit 1; fi

# Envoy's dependencies from its own uv.lock, with the hashes in it; then
# Envoy itself, built from the pinned source with a locked build backend
# and no further downloads.
COPY adapters/envoy/build-requirements.txt /src/build-requirements.txt
RUN set -eu; \
    python -m venv /opt/tools; \
    /opt/tools/bin/pip install --require-hashes --only-binary=:all: -r build-requirements.txt; \
    cd envoy; \
    /opt/tools/bin/uv export --frozen --no-dev --no-emit-project --format requirements-txt -o /src/requirements.txt; \
    python -m venv /opt/venv; \
    /opt/venv/bin/pip install --require-hashes --only-binary=:all: -r /src/requirements.txt; \
    /opt/venv/bin/pip install --require-hashes --only-binary=:all: -r /src/build-requirements.txt; \
    /opt/venv/bin/pip install --no-deps --no-build-isolation --no-index .; \
    /opt/venv/bin/pip uninstall -y uv; \
    /opt/venv/bin/pip check; \
    DATABASE_URL=postgresql+asyncpg://placeholder:placeholder@placeholder/placeholder \
        ADMIN_USERNAME=placeholder ADMIN_PASSWORD=placeholder \
        /opt/venv/bin/python -c "import envoy.server.main, envoy.admin.main"; \
    mkdir -p /out/envoy; \
    cp -r src/envoy/server/alembic src/envoy/server/alembic.ini /out/envoy/

# Licences of Envoy and of everything installed beside it.
COPY adapters/common/collect_licenses.py /src/collect_licenses.py
RUN set -eu; \
    /opt/venv/bin/python collect_licenses.py /out/licenses envoy; \
    mkdir -p /out/licenses/github.com/bsgip/envoy; \
    cp envoy/LICENSE.txt /out/licenses/github.com/bsgip/envoy/

FROM ${BASE_REGISTRY}/python:3.13-slim-trixie@${PYTHON_DIGEST} AS runtime

ARG ADAPTER_VERSION=dev
ARG UPSTREAM_VERSION=v2.5.0
ARG UPSTREAM_SHA=9d00340b5592f1fdf09dc74bd743fc251da0bfb5
# Debian trixie packages: the TLS proxy and the database Envoy needs.
ARG HAPROXY_VERSION=3.0.11-1+deb13u4
ARG POSTGRESQL_VERSION=17.11-0+deb13u1

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        "haproxy=${HAPROXY_VERSION}" "postgresql-17=${POSTGRESQL_VERSION}" \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin interop \
    && mkdir -p /run/ieee20305-interop \
    && chown interop /run/ieee20305-interop

COPY --from=build /opt/venv /opt/venv
COPY --from=build /out/envoy /opt/envoy
COPY --from=build /out/licenses /licenses
COPY LICENSE /licenses/ieee20305-interop/LICENSE
COPY adapters/envoy/THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md
COPY adapters/envoy/adapter/ieee20305_interop.py /opt/ieee20305-interop/ieee20305_interop.py
COPY fixtures/ /fixtures/
RUN printf '#!/bin/sh\nexec /opt/venv/bin/python -I /opt/ieee20305-interop/ieee20305_interop.py "$@"\n' \
        > /usr/local/bin/ieee20305-interop \
    && chmod 0755 /usr/local/bin/ieee20305-interop

ENV IEEE20305_INTEROP_ADAPTER_VERSION=${ADAPTER_VERSION} \
    IEEE20305_INTEROP_UPSTREAM_VERSION=${UPSTREAM_VERSION} \
    IEEE20305_INTEROP_UPSTREAM_REVISION=${UPSTREAM_SHA} \
    PATH="/usr/lib/postgresql/17/bin:${PATH}" \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

LABEL org.opencontainers.image.source="https://github.com/otfabric/ieee20305-interop" \
      org.opencontainers.image.title="ieee20305-interop-envoy" \
      org.opencontainers.image.description="ANU Envoy IEEE 2030.5 / CSIP-AUS server adapter for ieee20305-interop" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${ADAPTER_VERSION}"

EXPOSE 443

HEALTHCHECK --interval=2s --timeout=2s --retries=15 --start-period=40s \
    CMD ["test", "-f", "/run/ieee20305-interop/ready"]

USER 10001

ENTRYPOINT ["ieee20305-interop"]
CMD ["server"]

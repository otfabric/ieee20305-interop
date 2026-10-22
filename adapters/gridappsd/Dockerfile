# SPDX-License-Identifier: MIT
# ieee20305-interop adapter for GridAPPS-D / PNNL IEEE 2030.5
# implementation (Go, Battelle BSD).
#
# Build from the repository root:
#   docker build -f adapters/gridappsd/Dockerfile -t ieee20305-interop-gridappsd:dev .
#
# The GridAPPS-D project (PNNL / Battelle) provides both a server
# (ieee-2030_5-server-go) and a client (ieee-2030_5-client-go) in Go under
# the Battelle BSD licence. The adapter wraps both in the ieee20305-interop
# container contract. Pins are mirrored in versions.yaml.
#
# STATUS: skeleton — adapter code is not yet written.

ARG BASE_REGISTRY=docker.io/library
# golang:1.22-trixie and debian:trixie-slim, multi-arch indexes
# (linux/amd64 and linux/arm64).
ARG GOLANG_DIGEST=sha256:TODO_PIN_GOLANG_TRIXIE_DIGEST
ARG DEBIAN_DIGEST=sha256:TODO_PIN_DEBIAN_TRIXIE_SLIM_DIGEST

ARG UPSTREAM_VERSION=main
ARG UPSTREAM_SHA=TODO

# ---------------------------------------------------------------------------
# Stage 1: build the GridAPPS-D server and client, and the adapter
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/golang:1.22-trixie@${GOLANG_DIGEST} AS build

ARG UPSTREAM_VERSION
ARG UPSTREAM_SHA

WORKDIR /src

# Clone both the server and client repositories at the pinned version.
RUN git clone --depth 1 --branch "${UPSTREAM_VERSION}" \
        https://github.com/GRIDAPPSD/ieee-2030_5-server-go.git server \
    && git clone --depth 1 --branch "${UPSTREAM_VERSION}" \
        https://github.com/GRIDAPPSD/ieee-2030_5-client-go.git client \
    && if [ "${UPSTREAM_SHA}" != "TODO" ]; then \
           cd server && git fetch --depth 1 origin "${UPSTREAM_SHA}" && git checkout "${UPSTREAM_SHA}"; \
           cd ../client && git fetch --depth 1 origin "${UPSTREAM_SHA}" && git checkout "${UPSTREAM_SHA}"; \
       fi

# Build the server.
RUN cd server && go build -o /out/ieee20305-server ./...

# Build the client.
RUN cd client && go build -o /out/ieee20305-client ./...

# TODO: build the adapter entrypoint that wraps both the server and client
# in the ieee20305-interop container contract (commands: server, client,
# call, print-capabilities, print-fixture, version).
# COPY adapters/gridappsd/ /src/adapter/
# RUN cd /src/adapter && go build -o /out/ieee20305-interop .

# ---------------------------------------------------------------------------
# Stage 2: runtime image
# ---------------------------------------------------------------------------
FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST}

ARG ADAPTER_VERSION=dev

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Non-root user.
RUN groupadd -g 10001 interop && useradd -u 10001 -g interop -s /bin/false interop

# Readiness and control paths.
RUN mkdir -p /run/ieee20305-interop && chown 10001:10001 /run/ieee20305-interop

# Binaries from the build stage.
COPY --from=build /out/ieee20305-server /usr/local/bin/
COPY --from=build /out/ieee20305-client /usr/local/bin/
# TODO: COPY --from=build /out/ieee20305-interop /usr/local/bin/ieee20305-interop

# Fixtures baked into the image.
COPY fixtures/ /fixtures/

# Licences.
RUN mkdir -p /licenses/ieee20305-interop
COPY LICENSE /licenses/ieee20305-interop/LICENSE
# TODO: COPY THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md

# OCI labels.
LABEL org.opencontainers.image.source="https://github.com/otfabric/ieee20305-interop" \
      org.opencontainers.image.title="ieee20305-interop-gridappsd" \
      org.opencontainers.image.description="GridAPPS-D/PNNL IEEE 2030.5 server and client adapter for ieee20305-interop" \
      org.opencontainers.image.licenses="MIT AND BSD-3-Clause" \
      org.opencontainers.image.version="${ADAPTER_VERSION}"

EXPOSE 443

HEALTHCHECK --interval=2s --timeout=2s --retries=15 --start-period=20s \
    CMD ["test", "-f", "/run/ieee20305-interop/ready"]

USER 10001

ENTRYPOINT ["ieee20305-interop"]
CMD ["server"]

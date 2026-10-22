# SPDX-License-Identifier: MIT
# ieee20305-interop adapter for the GridAPPS-D IEEE 2030.5 server and client
# (Go, Battelle BSD). See README.md.
#
# Build from the repository root:
#   docker build -f adapters/gridappsd/Dockerfile -t ghcr.io/otfabric/ieee20305-interop-gridappsd:dev .
#
# Pins are mirrored in versions.yaml (make versions checks them).

# Where the base images come from. They are pinned by digest, so a mirror of
# Docker Hub serves exactly the same images.
ARG BASE_REGISTRY=docker.io/library
# golang:1.26.8-trixie and debian:trixie-slim, multi-arch indexes
# (linux/amd64 and linux/arm64).
ARG GOLANG_DIGEST=sha256:aae9c439b447d24c93ee483408992857cf39a3fb6ab7fe53affc65d834891fa8
ARG DEBIAN_DIGEST=sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad

FROM ${BASE_REGISTRY}/golang:1.26.8-trixie@${GOLANG_DIGEST} AS build

# The server release and its commit, and the client release and its commit.
ARG UPSTREAM_VERSION=v0.18.0
ARG UPSTREAM_SHA=d7a4a17042af7506610a973c6be8a5466db34053
ARG CLIENT_VERSION=v0.1.0
ARG CLIENT_SHA=66570b94ea104b46eb12b48ef2451b3454050176
ARG ADAPTER_VERSION=dev
ARG GOPROXY=https://proxy.golang.org

ENV CGO_ENABLED=0 GOPROXY=${GOPROXY} GOSUMDB=sum.golang.org GOTOOLCHAIN=local
WORKDIR /src

# The upstreams at their pinned commits. A tag that moved fails the build.
RUN set -eu; \
    for pin in "server ieee-2030_5-server-go ${UPSTREAM_VERSION} ${UPSTREAM_SHA}" \
               "client ieee-2030_5-client-go ${CLIENT_VERSION} ${CLIENT_SHA}"; do \
        set -- ${pin}; \
        echo "$4" | grep -Eq '^[0-9a-f]{40}$'; \
        git clone --quiet --depth 1 --branch "$3" "https://github.com/GRIDAPPSD/$2.git" "$1"; \
        got="$(git -C "$1" rev-parse HEAD)"; \
        if [ "${got}" != "$4" ]; then echo "$2 $3 is commit ${got}, not $4" >&2; exit 1; fi; \
    done

# The server, from its vendored modules.
RUN set -eu; \
    cd server; \
    go build -mod=vendor -trimpath -ldflags "-s -w" -o /out/sep2server ./cmd/sep2server

# The adapter, built inside the client module, whose protocol engine it
# drives (internal/inverter). Modules are downloaded and checked against the
# client's go.sum.
COPY adapters/gridappsd/adapter/ client/cmd/ieee20305-interop/
RUN set -eu; \
    cd client; \
    test -z "$(gofmt -l cmd/ieee20305-interop)"; \
    go mod download && go mod verify; \
    go vet -mod=readonly ./cmd/ieee20305-interop; \
    go build -mod=readonly -trimpath -ldflags "-s -w \
        -X main.adapterVersion=${ADAPTER_VERSION} \
        -X main.serverVersion=${UPSTREAM_VERSION} \
        -X main.serverRevision=${UPSTREAM_SHA} \
        -X main.clientVersion=${CLIENT_VERSION} \
        -X main.clientRevision=${CLIENT_SHA}" \
        -o /out/ieee20305-interop ./cmd/ieee20305-interop

# Licences of everything in the two binaries.
RUN set -eu; \
    mkdir -p /out/licenses/go; \
    cp "$(go env GOROOT)/LICENSE" /out/licenses/go/LICENSE; \
    for repo in server client; do \
        mkdir -p "/out/licenses/github.com/GRIDAPPSD/ieee-2030_5-${repo}-go"; \
        cp "${repo}"/LICENSE* "${repo}"/NOTICE* "/out/licenses/github.com/GRIDAPPSD/ieee-2030_5-${repo}-go/" 2>/dev/null || true; \
        test -n "$(ls "/out/licenses/github.com/GRIDAPPSD/ieee-2030_5-${repo}-go")"; \
    done; \
    { go version -m /out/sep2server; go version -m /out/ieee20305-interop; } \
        | awk '$1 == "dep" { print $2, $3 }' | sort -u > /out/licenses/BUNDLED-MODULES.txt; \
    test -s /out/licenses/BUNDLED-MODULES.txt; \
    while read -r module modversion; do \
        dir="$(cd client && go list -m -f '{{.Dir}}' "${module}" 2>/dev/null || true)"; \
        if [ -z "${dir}" ] || [ ! -d "${dir}" ]; then dir="server/vendor/${module}"; fi; \
        mkdir -p "/out/licenses/${module}"; \
        for file in "${dir}"/LICENSE* "${dir}"/LICENCE* "${dir}"/COPYING* "${dir}"/NOTICE*; do \
            if [ -f "${file}" ]; then cp "${file}" "/out/licenses/${module}/"; fi; \
        done; \
        test -n "$(ls "/out/licenses/${module}")" || { echo "no licence file in ${module} ${modversion}" >&2; exit 1; }; \
    done < /out/licenses/BUNDLED-MODULES.txt

FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST} AS runtime

ARG ADAPTER_VERSION=dev

RUN useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin interop \
    && mkdir -p /run/ieee20305-interop \
    && chown interop /run/ieee20305-interop

COPY --from=build /out/sep2server /usr/local/bin/sep2server
COPY --from=build /out/ieee20305-interop /usr/local/bin/ieee20305-interop
COPY --from=build /out/licenses /licenses
COPY LICENSE /licenses/ieee20305-interop/LICENSE
COPY adapters/gridappsd/THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md
COPY fixtures/ /fixtures/

LABEL org.opencontainers.image.source="https://github.com/otfabric/ieee20305-interop" \
      org.opencontainers.image.title="ieee20305-interop-gridappsd" \
      org.opencontainers.image.description="GridAPPS-D IEEE 2030.5 server and client adapter for ieee20305-interop" \
      org.opencontainers.image.licenses="MIT AND LicenseRef-Battelle-BSD" \
      org.opencontainers.image.version="${ADAPTER_VERSION}"

EXPOSE 443

HEALTHCHECK --interval=2s --timeout=2s --retries=15 --start-period=20s \
    CMD ["test", "-f", "/run/ieee20305-interop/ready"]

USER 10001

ENTRYPOINT ["ieee20305-interop"]
CMD ["server"]

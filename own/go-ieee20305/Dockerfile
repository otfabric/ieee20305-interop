# SPDX-License-Identifier: MIT
#
# ieee20305-interop-otfabric: OT Fabric's own IEEE 2030.5 stack behind the
# container contract. Not an independent implementation: see README.md.
#
# Build from the repository root:
#   make -f own/go-ieee20305/Makefile image
#
# The build takes the go-ieee20305 release as UPSTREAM_VERSION (the released
# git tag, which is the Go module version) and its commit as UPSTREAM_SHA.
# There is no pin in this repository: the workflow passes the latest release.
# A build never uses main. go.mod and go.sum here are only a starting point
# for local work; the CCM_8 module (go-ieee20305/tlsccm) is the version go.mod
# names, which go get keeps unless the release needs a newer one.

ARG BASE_REGISTRY=docker.io/library
# golang:1.26.8-trixie and debian:trixie-slim, multi-arch indexes
# (linux/amd64 and linux/arm64): the digests of versions.yaml.
ARG GOLANG_DIGEST=sha256:aae9c439b447d24c93ee483408992857cf39a3fb6ab7fe53affc65d834891fa8
ARG DEBIAN_DIGEST=sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad

FROM ${BASE_REGISTRY}/golang:1.26.8-trixie@${GOLANG_DIGEST} AS build

ARG UPSTREAM_VERSION
ARG UPSTREAM_SHA
ARG ADAPTER_VERSION=dev
ARG GOPROXY=https://proxy.golang.org

ENV CGO_ENABLED=0 GOPROXY=${GOPROXY} GOSUMDB=sum.golang.org GOTOOLCHAIN=local
WORKDIR /src
COPY own/go-ieee20305/go.mod own/go-ieee20305/go.sum own/go-ieee20305/*.go ./

RUN set -eu; \
    test -n "${UPSTREAM_VERSION}" || { echo "UPSTREAM_VERSION is required" >&2; exit 1; }; \
    echo "${UPSTREAM_SHA}" | grep -Eq '^[0-9a-f]{40}$'; \
    go get "github.com/otfabric/go-ieee20305@${UPSTREAM_VERSION}"; \
    go mod tidy; \
    built="$(go list -m -f '{{.Version}}' github.com/otfabric/go-ieee20305)"; \
    test "${built}" = "${UPSTREAM_VERSION}" || { echo "go.mod has go-ieee20305 ${built}, want ${UPSTREAM_VERSION}" >&2; exit 1; }; \
    go mod download && go mod verify; \
    origin="$(go mod download -json "github.com/otfabric/go-ieee20305@${UPSTREAM_VERSION}" \
        | sed -nE 's/^[[:space:]]*"Hash": "([0-9a-f]{40})".*/\1/p')"; \
    if [ -n "${origin}" ] && [ "${origin}" != "${UPSTREAM_SHA}" ]; then \
        echo "go-ieee20305 ${UPSTREAM_VERSION} is commit ${origin}, not ${UPSTREAM_SHA}" >&2; exit 1; \
    fi

# The licences: every LICENSE, NOTICE and THIRD_PARTY_NOTICES file of each
# module in the binary, with its path in the module (tlsccm keeps those of
# the code it carries in internal/).
RUN set -eu; \
    test -z "$(gofmt -l .)"; \
    go vet ./...; \
    go test ./...; \
    go build -trimpath -ldflags "-s -w \
        -X main.adapterVersion=${ADAPTER_VERSION} \
        -X main.upstreamVersion=${UPSTREAM_VERSION} \
        -X main.upstreamRevision=${UPSTREAM_SHA}" \
        -o /out/ieee20305-interop .; \
    mkdir -p /out/licenses/go; \
    cp "$(go env GOROOT)/LICENSE" /out/licenses/go/LICENSE; \
    go version -m /out/ieee20305-interop | awk '$1 == "dep" { print $2, $3 }' > /out/licenses/BUNDLED-MODULES.txt; \
    test -s /out/licenses/BUNDLED-MODULES.txt; \
    while read -r module modversion; do \
        dir="$(go list -m -f '{{.Dir}}' "${module}")"; \
        mkdir -p "/out/licenses/${module}"; \
        (cd "${dir}" && find . -path ./testdata -prune -o -type f \
            \( -name 'LICENSE*' -o -name 'LICENCE*' -o -name 'COPYING*' -o -name 'NOTICE*' -o -name 'THIRD_PARTY_NOTICES*' \) -print) \
            | while read -r file; do \
                mkdir -p "/out/licenses/${module}/$(dirname "${file}")"; \
                cp "${dir}/${file}" "/out/licenses/${module}/${file}"; \
            done; \
        test -n "$(ls "/out/licenses/${module}")" || { echo "no licence file in ${module} ${modversion}" >&2; exit 1; }; \
    done < /out/licenses/BUNDLED-MODULES.txt

FROM ${BASE_REGISTRY}/debian:trixie-slim@${DEBIAN_DIGEST} AS runtime

ARG ADAPTER_VERSION=dev
ARG UPSTREAM_VERSION

RUN useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin interop \
    && mkdir -p /run/ieee20305-interop \
    && chown interop /run/ieee20305-interop

COPY --from=build /out/ieee20305-interop /usr/local/bin/ieee20305-interop
COPY --from=build /out/licenses /licenses
COPY LICENSE /licenses/ieee20305-interop/LICENSE
COPY THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md
COPY fixtures/ /fixtures/

LABEL org.opencontainers.image.title="ieee20305-interop-otfabric" \
      org.opencontainers.image.description="OT Fabric go-ieee20305 behind the ieee20305-interop container contract, for testing. Not an independent implementation." \
      org.opencontainers.image.source="https://github.com/otfabric/ieee20305-interop" \
      org.opencontainers.image.licenses="MIT AND BSD-3-Clause AND LicenseRef-Battelle-BSD" \
      org.opencontainers.image.version="${UPSTREAM_VERSION}"

USER 10001
EXPOSE 443

HEALTHCHECK --interval=2s --timeout=2s --start-period=10s --retries=15 \
    CMD ["test", "-f", "/run/ieee20305-interop/ready"]

ENTRYPOINT ["/usr/local/bin/ieee20305-interop"]
CMD ["server"]

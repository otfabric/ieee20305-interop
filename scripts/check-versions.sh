#!/usr/bin/env bash
# Fails when versions.yaml disagrees with what the builds actually pin.
#
# Every adapter Dockerfile declares its pins as build arguments with a default:
#   ARG UPSTREAM_VERSION=...     the upstream release
#   ARG UPSTREAM_SHA=...         its commit
#   ARG <NAME>_DIGEST=sha256:... every base image
# Each of those values must appear in versions.yaml, every FROM line must be
# pinned by digest, and every adapter that versions.yaml marks as built or
# published must have a Dockerfile.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"
status=0

need() {  # need <description> <value that must appear in versions.yaml>
    if grep -qF -- "$2" versions.yaml; then
        echo "ok   $1: $2"
    else
        echo "FAIL $1: $2 is not in versions.yaml"
        status=1
    fi
}

for dockerfile in adapters/*/Dockerfile; do
    [[ -f "${dockerfile}" ]] || continue
    adapter=$(basename "$(dirname "${dockerfile}")")
    version=$(sed -nE 's/^ARG UPSTREAM_VERSION=(.+)$/\1/p' "${dockerfile}" | sed -n 1p)
    if [[ -z "${version}" ]]; then
        echo "FAIL ${adapter}: the Dockerfile has no ARG UPSTREAM_VERSION=<pin>"
        status=1
    else
        need "${adapter} version" "version: \"${version}\""
    fi
    sha=$(sed -nE 's/^ARG UPSTREAM_SHA=(.+)$/\1/p' "${dockerfile}" | sed -n 1p)
    [[ -n "${sha}" ]] && need "${adapter} revision" "${sha}"
    while read -r name digest; do
        need "${adapter} ${name}" "${digest}"
    done < <(sed -nE 's/^ARG ([A-Z0-9_]+_DIGEST)=(sha256:[0-9a-f]{64})$/\1 \2/p' "${dockerfile}")
    if grep -nE '^FROM ' "${dockerfile}" | grep -vE '@(\$\{[A-Z0-9_]+_DIGEST\}|sha256:)|^[0-9]+:FROM [a-z]+( AS [a-z]+)?$' >/dev/null; then
        echo "FAIL ${adapter}: a FROM line is not pinned by digest"
        status=1
    fi
done

# An adapter that is built or published needs its Dockerfile.
while read -r adapter state; do
    if [[ "${state}" != "planned" && ! -f "adapters/${adapter}/Dockerfile" ]]; then
        echo "FAIL ${adapter}: status ${state} in versions.yaml, but adapters/${adapter}/Dockerfile does not exist"
        status=1
    else
        echo "ok   ${adapter}: status ${state}"
    fi
done < <(awk '/^implementations:/{in_impl=1; next} /^[a-z]/{in_impl=0}
              in_impl && /^  [a-z0-9]+:$/{name=$1; sub(":","",name)}
              in_impl && /^    status:/{print name, $2}' versions.yaml)

exit "${status}"

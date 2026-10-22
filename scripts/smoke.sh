#!/usr/bin/env bash
# Container-contract smoke test for one adapter image.
#
# Usage: smoke.sh <adapter>          e.g. smoke.sh gridappsd
#
# Checks the parts of docs/CONTAINER_CONTRACT.md that do not need a second
# adapter: the command surface, usage errors and exit codes, capabilities,
# the baked fixtures, readiness, the event stream, a clean stop on SIGTERM,
# and mutual TLS. Where a check needs a peer, an adapter with both roles
# talks to itself; one with a single role uses SMOKE_PEER for the other.
#
# Environment: REGISTRY, VERSION (image tag, default dev), VERBOSE=1,
#              REPORT_DIR (keeps the capability document when set),
#              SMOKE_PEER (default gridappsd, which has both roles).

set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
# shellcheck source=scripts/lib.sh
source "${SCRIPT_DIR}/lib.sh"

ADAPTER="${1:?usage: smoke.sh <adapter>}"
IMAGE="$(image_of "${ADAPTER}")"
PEER="${SMOKE_PEER:-gridappsd}"
FIXTURE="${ROOT}/fixtures/baseline/fixture.json"
SERIAL=$(jq -r '.device.serial' "${FIXTURE}")
require_tools
WORK=$(mktemp -d)
trap 'rm -rf "${WORK}"; cleanup' EXIT
network_up
log "${IMAGE}"

expect_exit() {  # expect_exit <code> <label> <docker run args...>
    local want="$1" label="$2"; shift 2
    docker run --rm --network "${NETWORK}" --label "ieee20305-interop.run=${RUN_ID}" "$@" >/dev/null 2>&1
    local got=$?
    if [[ "${got}" == "${want}" ]]; then ok "${label}: exit ${got}"; else fail "${label}: exit ${got}, want ${want}"; fi
}

# ---- capabilities ----
caps=$(docker run --rm "${IMAGE}" print-capabilities 2>/dev/null)
if [[ -n "${REPORT_DIR:-}" ]]; then
    mkdir -p "${REPORT_DIR}"
    printf '%s\n' "${caps}" > "${REPORT_DIR}/capabilities-${ADAPTER}.json"
fi
if jq -e --arg a "${ADAPTER}" '
        .schemaVersion == "1.0" and .adapter == $a and (.adapterVersion | type == "string")
        and (.contractVersion | test("^[0-9]+\\.[0-9]+$"))
        and (.upstream.revision | test("^[0-9a-f]{40}$")) and (.upstream.version | type == "string")
        and (.tls.cipherSuites | type == "array" and length > 0)
        and (.versions | length) > 0
        and ([.versions[] | (.client.supported | type == "boolean") and (.server.supported | type == "boolean")] | all)' \
        <<<"${caps}" >/dev/null 2>&1
then ok "print-capabilities"; else fail "print-capabilities is not a valid capability document: ${caps}"; fi
if [[ $(wc -l <<<"${caps}") -eq 1 ]]; then ok "print-capabilities is one line"; else fail "print-capabilities printed more than one line"; fi

# Capability documents for known_bug (scripts/lib.sh).
printf '%s\n' "${caps}" > "${WORK}/caps-${ADAPTER}.json"
docker run --rm "$(image_of "${PEER}")" print-capabilities > "${WORK}/caps-${PEER}.json" 2>/dev/null

has_role() { jq -e --arg v "$2" --arg r "$1" '.versions[$v][$r].supported == true' <<<"${caps}" >/dev/null 2>&1; }
DECLARED=$(jq -r '.versions | keys[]' <<<"${caps}" 2>/dev/null | tr '\n' ' ')
log "declared IEEE 2030.5 versions: ${DECLARED}"

# ---- the baked fixtures are the repository's ----
if diff -q <(docker run --rm "${IMAGE}" print-fixture baseline 2>/dev/null) "${FIXTURE}" >/dev/null
then ok "print-fixture baseline"; else fail "print-fixture baseline differs from fixtures/baseline/fixture.json"; fi
expect_exit 1 "print-fixture of an unknown name" "${IMAGE}" print-fixture no-such-fixture
expect_exit 0 "version" "${IMAGE}" version
if [[ $(docker run --rm "${IMAGE}" version 2>/dev/null | wc -l) -eq 1 ]]; then ok "version is one line"; else fail "version is not one line"; fi

labels=$(docker image inspect --format '{{json .Config.Labels}}' "${IMAGE}")
if jq -e '.["org.opencontainers.image.source"] and .["org.opencontainers.image.licenses"] and .["org.opencontainers.image.title"]' <<<"${labels}" >/dev/null 2>&1
then ok "OCI labels"; else fail "OCI labels missing: ${labels}"; fi
if docker run --rm --entrypoint sh "${IMAGE}" -c 'test -f /licenses/THIRD_PARTY_NOTICES.md && test -f /licenses/ieee20305-interop/LICENSE' >/dev/null 2>&1
then ok "licences under /licenses"; else fail "/licenses lacks THIRD_PARTY_NOTICES.md or the adapter licence"; fi

# ---- usage errors never touch the network ----
expect_exit 2 "no command" "${IMAGE}" ""
expect_exit 2 "unknown command" "${IMAGE}" frobnicate
expect_exit 2 "server without --version" "${IMAGE}" server --tls-dir /tls
expect_exit 2 "server with an unknown flag" "${IMAGE}" server --version 2018 --tls-dir /tls --frobnicate 1
expect_exit 2 "server with a flag given twice" "${IMAGE}" server --version 2018 --version 2018 --tls-dir /tls
expect_exit 2 "server with an unknown version" "${IMAGE}" server --version 9999 --tls-dir /tls
expect_exit 2 "server without --tls-dir" "${IMAGE}" server --version 2018
expect_exit 2 "client without --url" "${IMAGE}" client register --version 2018 --tls-dir /tls
expect_exit 2 "client without --version" "${IMAGE}" client register --url https://nowhere:443/dcap --tls-dir /tls
expect_exit 2 "client with a URL that is not https://" "${IMAGE}" client register --version 2018 --url http://nowhere/dcap --tls-dir /tls
expect_exit 2 "client with an unknown operation" "${IMAGE}" client frobnicate --version 2018 --url https://nowhere:443/dcap --tls-dir /tls

# ---- TLS material for everything below ----
servers=("srv-${RUN_ID}" "peer-${RUN_ID}")
make_pki "${WORK}/pki" "${SERIAL}" "${servers[@]}" localhost || { fail "cannot generate the TLS material"; finish; exit 1; }
TLS=(-v "${WORK}/pki:/tls:ro")
for v in ${DECLARED}; do
    has_role server "${v}" || continue
    expect_exit 4 "server with a --tls-dir that has no server certificate" "${TLS[@]}" "${IMAGE}" server --version "${v}" --tls-dir /tls/client
    break
done

for v in ${DECLARED}; do
    CHECK_PREFIX="[${v}] "
    log "IEEE 2030.5 ${v}"
    server_image="${ADAPTER}"; server_name="srv-${RUN_ID}"
    if ! has_role server "${v}"; then server_image="${PEER}"; server_name="peer-${RUN_ID}"; fi

    start "${server_name}" "${server_image}" "${TLS[@]}" -- server --version "${v}" --tls-dir /tls/server
    if ! wait_ready "${server_name}"; then fail "${server_image} server did not become ready"; stop_container "${server_name}"; continue; fi

    if has_role server "${v}"; then
        ok "server becomes ready"
        ready=$(events "${server_name}" | jq -c 'select(.event == "ready")' 2>/dev/null | sed -n 1p)
        if jq -e --arg a "${ADAPTER}" --arg v "${v}" '.adapter == $a and .version == $v and .port == 443 and (.time | type == "string")' <<<"${ready}" >/dev/null 2>&1
        then ok "ready event"; else fail "no valid ready event: ${ready}"; fi
        user=$(docker exec "${server_name}" id -u 2>/dev/null)
        if [[ "${user}" == 10001 ]]; then ok "runs as uid 10001"; else fail "runs as uid ${user}, want 10001"; fi
    fi

    if has_role client "${v}"; then
        url="https://${server_name}:443/dcap"
        for op in $(jq -r --arg v "${v}" '.versions[$v].client.operations[]' <<<"${caps}"); do
            doc=$(run_client "${ADAPTER}" "${TLS[@]}" -- "${op}" --version "${v}" --url "${url}" --tls-dir /tls/client); code=$?
            if [[ "${code}" == 0 ]] && jq -e --arg op "${op}" '.ok == true and .operation == $op and .schemaVersion == "1.0"' <<<"${doc}" >/dev/null 2>&1
            then ok "client ${op} against ${server_image}"
            elif bug=$(known_bug "${ADAPTER}" "${server_image}" "${v}.${op}") \
                 && jq -e --arg op "${op}" '.ok == false and .operation == $op and .schemaVersion == "1.0"' <<<"${doc}" >/dev/null 2>&1
            then ok "client ${op} against ${server_image}: known issue (tests/known-bugs.tsv line ${bug%%$'\t'*}), a valid result document"
            else fail "client ${op} against ${server_image}: exit ${code}, ${doc:0:300}"; fi
        done
        doc=$(run_client "${ADAPTER}" "${TLS[@]}" -- discover --version "${v}" --url "https://no-such-host-${RUN_ID}:443/dcap" --tls-dir /tls/client --connect-timeout-ms 1500); code=$?
        if [[ "${code}" == 3 ]] && jq -e '.ok == false and .error.code == "connect-failed" and .response == null' <<<"${doc}" >/dev/null 2>&1
        then ok "unreachable server: exit 3 and a result document"; else fail "unreachable server: exit ${code}, ${doc:0:300}"; fi
        doc=$(run_client "${ADAPTER}" "${TLS[@]}" -- discover --version "${v}" --url "${url}" --tls-dir /tls/other-ca); code=$?
        if [[ "${code}" == 3 ]] && jq -e '.error.code == "connect-failed"' <<<"${doc}" >/dev/null 2>&1
        then ok "mTLS: a device certificate of another CA is refused"; else fail "mTLS: device of another CA: exit ${code}, ${doc:0:300}"; fi
        doc=$(run_client "${ADAPTER}" "${TLS[@]}" -- discover --version "${v}" --url "${url}" --tls-dir /tls/untrusted); code=$?
        if [[ "${code}" == 3 ]] && jq -e '.error.code == "connect-failed"' <<<"${doc}" >/dev/null 2>&1
        then ok "mTLS: a server certificate the client does not trust is refused"; else fail "mTLS: untrusted server: exit ${code}, ${doc:0:300}"; fi
    fi

    if has_role server "${v}"; then
        docker stop -t 15 "${server_name}" >/dev/null
        code=$(docker inspect --format '{{.State.ExitCode}}' "${server_name}")
        exited "${server_name}"
        if [[ "${code}" == 0 ]]; then ok "SIGTERM: exit 0"; else fail "SIGTERM: exit ${code}"; fi
        if events "${server_name}" | tail -1 | jq -e '.event == "stopped"' >/dev/null 2>&1; then ok "stopped event"; else fail "the last stdout line is not the stopped event"; fi
        if events "${server_name}" | grep -v '^{' | grep -q .; then fail "server stdout has lines that are not JSON"; else ok "server stdout is JSON Lines only"; fi
    fi
    stop_container "${server_name}"
done

finish

#!/usr/bin/env bash
# Container-contract smoke test for one adapter image.
#
# Usage: smoke.sh <adapter>          e.g. smoke.sh epri
#
# Checks the parts of docs/CONTAINER_CONTRACT.md that do not need a second
# adapter: the command surface, usage errors and exit codes, capabilities, the
# baked fixtures, fixture validation, readiness, the event stream, the control
# socket, timeouts and a clean stop on SIGTERM. Where a check needs a peer,
# the image talks to itself; scripts/interop.sh pairs different adapters.
#
# An adapter without a server role (epri, py20305: client only) runs the
# client checks against the server of SMOKE_PEER. An adapter without a client
# role runs the server checks with the client of SMOKE_PEER.
#
# Environment: REGISTRY, VERSION (image tag, default dev), VERBOSE=1,
#              REPORT_DIR (keeps the capability document when set),
#              SMOKE_PEER (default envoy, which serves as an IEEE 2030.5 server).

set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
# shellcheck source=scripts/lib.sh
source "${SCRIPT_DIR}/lib.sh"

ADAPTER="${1:?usage: smoke.sh <adapter>}"
IMAGE="$(image_of "${ADAPTER}")"
PEER="${SMOKE_PEER:-envoy}"
FIXTURE="${ROOT}/fixtures/baseline/fixture.json"
DEVICE_ID=$(jq -r '.endDevices[0].lFDI' "${FIXTURE}")
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
        and (.versions | length) > 0
        and ([.versions[] | (.client.supported | type == "boolean") and (.server.supported | type == "boolean")] | all)' \
        <<<"${caps}" >/dev/null 2>&1
then ok "print-capabilities"; else fail "print-capabilities is not a valid capability document: ${caps}"; fi
if [[ $(wc -l <<<"${caps}") -eq 1 ]]; then ok "print-capabilities is one line"; else fail "print-capabilities printed more than one line"; fi

has_role() { jq -e --arg v "$2" --arg r "$1" '.versions[$v][$r].supported == true' <<<"${caps}" >/dev/null 2>&1; }
DECLARED=$(jq -r '.versions | keys[]' <<<"${caps}" 2>/dev/null | tr '\n' ' ')
log "declared IEEE 2030.5 versions: ${DECLARED}"

# ---- the baked fixtures are the repository's ----
if diff -q <(docker run --rm "${IMAGE}" print-fixture baseline 2>/dev/null) "${FIXTURE}" >/dev/null
then ok "print-fixture baseline"; else fail "print-fixture baseline differs from fixtures/baseline/fixture.json"; fi
expect_exit 1 "print-fixture of an unknown name" "${IMAGE}" print-fixture no-such-fixture
expect_exit 0 "version" "${IMAGE}" version
if [[ $(docker run --rm "${IMAGE}" version 2>/dev/null | wc -l) -eq 1 ]]; then ok "version is one line"; else fail "version is not one line"; fi

# ---- usage errors and unsupported requests never touch the network ----
expect_exit 2 "no command" "${IMAGE}" ""
expect_exit 2 "unknown command" "${IMAGE}" frobnicate
expect_exit 2 "server without --version" "${IMAGE}" server
expect_exit 2 "server with an unknown flag" "${IMAGE}" server --version 2018 --frobnicate 1
expect_exit 2 "server with an unknown version" "${IMAGE}" server --version 9999
expect_exit 2 "client without --url" "${IMAGE}" client register --version 2018
expect_exit 2 "client without --version" "${IMAGE}" client register --url https://nowhere:443/dcap
expect_exit 2 "client with a URL that is not https://" "${IMAGE}" client register --version 2018 --url http://nowhere/dcap

# ---- server and client checks per declared version ----
for v in ${DECLARED}; do
    CHECK_PREFIX="[${v}] "
    log "IEEE 2030.5 ${v}"

    # ---- server checks (if this adapter has a server role) ----
    if has_role server "${v}"; then
        srv="srv-${RUN_ID}"
        start "${srv}" "${ADAPTER}" -- server --version "${v}"
        if wait_ready "${srv}"; then ok "server becomes ready"; else fail "server did not become ready"; continue; fi

        first=$(events "${srv}" | sed -n 1p)
        if jq -e --arg a "${ADAPTER}" --arg v "${v}" '.event == "ready" and .adapter == $a and .version == $v and .port == 443 and (.time | type == "string")' <<<"${first}" >/dev/null 2>&1
        then ok "ready event"; else fail "first stdout line is not the ready event: ${first}"; fi

        user=$(docker exec "${srv}" id -u 2>/dev/null)
        if [[ "${user}" == 10001 ]]; then ok "runs as uid 10001"; else fail "runs as uid ${user}, want 10001"; fi

        labels=$(docker image inspect --format '{{json .Config.Labels}}' "${IMAGE}")
        if jq -e '.["org.opencontainers.image.source"] and .["org.opencontainers.image.licenses"] and .["org.opencontainers.image.title"]' <<<"${labels}" >/dev/null 2>&1
        then ok "OCI labels"; else fail "OCI labels missing: ${labels}"; fi

        docker stop -t 15 "${srv}" >/dev/null
        code=$(docker inspect --format '{{.State.ExitCode}}' "${srv}")
        exited "${srv}"
        last=$(events "${srv}" | tail -1)
        if [[ "${code}" == 0 ]]; then ok "SIGTERM: exit 0"; else fail "SIGTERM: exit ${code}"; fi
        if jq -e '.event == "stopped"' <<<"${last}" >/dev/null 2>&1; then ok "stopped event"; else fail "last stdout line is not the stopped event: ${last}"; fi
        stop_container "${srv}"
    fi

    # ---- client checks (if this adapter has a client role) ----
    if has_role client "${v}"; then
        # A connection that cannot be made.
        doc=$(run_client "${ADAPTER}" register --version "${v}" --url "https://no-such-host-${RUN_ID}:443/dcap" --connect-timeout-ms 1500); code=$?
        if [[ "${code}" == 3 ]] && jq -e '.ok == false and .error.code == "connect-failed" and .response == null' <<<"${doc}" >/dev/null 2>&1
        then ok "connect failure: exit 3 and a result document"; else fail "connect failure: exit ${code}, ${doc}"; fi
    fi

    # ---- client operations against self or peer ----
    if has_role server "${v}" && has_role client "${v}"; then
        srv="opsrv-${RUN_ID}"
        start "${srv}" "${ADAPTER}" -- server --version "${v}"
        wait_ready "${srv}" || { fail "server did not become ready for operation tests"; continue; }

        # Test each client operation
        for op in register discover poll-controls report; do
            doc=$(run_client "${ADAPTER}" "${op}" --version "${v}" --url "https://${srv}:443/dcap"); code=$?
            if [[ "${code}" == 0 ]] && jq -e '.ok == true' <<<"${doc}" >/dev/null 2>&1
            then ok "client ${op}"; else fail "client ${op}: exit ${code}, ${doc}"; fi
        done
        stop_container "${srv}"
    elif has_role client "${v}"; then
        srv="peersrv-${RUN_ID}"
        start "${srv}" "${PEER}" -- server --version "${v}"
        wait_ready "${srv}" || { fail "${PEER} server did not become ready"; continue; }

        for op in register discover poll-controls report; do
            doc=$(run_client "${ADAPTER}" "${op}" --version "${v}" --url "https://${srv}:443/dcap"); code=$?
            if [[ "${code}" == 0 ]] && jq -e '.ok == true' <<<"${doc}" >/dev/null 2>&1
            then ok "client ${op} against ${PEER}"; else fail "client ${op} against ${PEER}: exit ${code}, ${doc}"; fi
        done
        stop_container "${srv}"
    fi

    if events "${srv}" 2>/dev/null | grep -v '^{' | grep -q .; then fail "server stdout has lines that are not JSON"; else ok "server stdout is JSON Lines only"; fi
done

# ---- mTLS checks ----
for v in ${DECLARED}; do
    has_role server "${v}" || continue
    CHECK_PREFIX="[${v}][mTLS] "
    log "IEEE 2030.5 ${v} mTLS"
    dir="${WORK}/tls"
    if [[ ! -d "${dir}" ]]; then
        make_pki "${dir}/ec" ec "${DEVICE_ID}" "tls-${RUN_ID}" localhost 127.0.0.1 \
            || { fail "cannot generate the TLS material"; continue; }
    fi
    tls="/tls/ec"
    srv="tls-${RUN_ID}"
    start "${srv}" "${ADAPTER}" -v "${WORK}/tls:/tls:ro" -- server --version "${v}" --tls-dir "${tls}/server"
    if ! wait_ready "${srv}"; then fail "mTLS server did not become ready"; stop_container "${srv}"; continue; fi

    if has_role client "${v}"; then
        # Valid client certificate.
        doc=$(run_client "${ADAPTER}" -v "${WORK}/tls:/tls:ro" -- register --version "${v}" \
            --url "https://tls-${RUN_ID}:443/dcap" --tls-dir "${tls}/client" --connect-timeout-ms 5000); code=$?
        if [[ "${code}" == 0 ]] && jq -e '.ok == true' <<<"${doc}" >/dev/null 2>&1
        then ok "mTLS: accepted with valid certificate"; else fail "mTLS: valid certificate: exit ${code}, ${doc}"; fi

        # Client certificate from another CA.
        doc=$(run_client "${ADAPTER}" -v "${WORK}/tls:/tls:ro" -- register --version "${v}" \
            --url "https://tls-${RUN_ID}:443/dcap" --tls-dir "${tls}/other-ca" --connect-timeout-ms 5000); code=$?
        if [[ "${code}" == 3 ]] && jq -e '.ok == false and .error.code == "connect-failed"' <<<"${doc}" >/dev/null 2>&1
        then ok "mTLS: rejected client from unknown CA"; else fail "mTLS: unknown CA: exit ${code}, ${doc}"; fi

        # No client certificate (server should refuse).
        doc=$(run_client "${ADAPTER}" -v "${WORK}/tls:/tls:ro" -- register --version "${v}" \
            --url "https://tls-${RUN_ID}:443/dcap" --tls-dir "${tls}/untrusted" --connect-timeout-ms 5000); code=$?
        if [[ "${code}" == 3 ]] && jq -e '.ok == false and .error.code == "connect-failed"' <<<"${doc}" >/dev/null 2>&1
        then ok "mTLS: rejected untrusted server certificate"; else fail "mTLS: untrusted server: exit ${code}, ${doc}"; fi
    fi
    stop_container "${srv}"
done

finish

#!/usr/bin/env bash
# Reference-stack self-test: every client adapter against every server adapter,
# with operations from both ends of the connection.
#
# Usage: interop.sh <adapter> [<adapter> ...]      e.g. interop.sh epri envoy py20305 gridappsd
#
# For each IEEE 2030.5 version both adapters declare (2018, 2023), and each
# pairing of a client and a server, on a private Docker network:
#
#   register       the client registers an EndDevice with the server
#   discover       the client discovers the DeviceCapability resource
#   poll-controls  the client polls for DER controls
#   report         the client sends a metering report
#   subscribe      the client subscribes to server-pushed notifications
#   mTLS           mutual TLS with device certificates (LFDI/SFDI)
#
# Every result is a report result with the status PASS, FAIL,
# SKIP_UNSUPPORTED (a capability one side does not declare),
# KNOWN_ISSUE (a failure that tests/known-bugs.tsv explains) or INFRA_ERROR.
#
# Environment: REGISTRY, VERSION (image tag, default dev), VERBOSE=1,
#              REPORT_DIR (keeps every result document and the report),
#              IEEE20305_VERSIONS (default "2018 2023"),
#              INTEROP_PAIRS: run only the pairings whose server and version
#              are listed, as "<server>@<version>" separated by spaces or
#              commas (default: every one).

set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
# shellcheck source=scripts/lib.sh
source "${SCRIPT_DIR}/lib.sh"

[[ $# -ge 1 ]] || { echo "usage: interop.sh <adapter> [<adapter> ...]" >&2; exit 2; }
ADAPTERS=("$@")
read -r -a VERSIONS <<<"${IEEE20305_VERSIONS:-2018 2023}"
INTEROP_PAIRS="${INTEROP_PAIRS:-}"
read -r -a PAIRS <<<"${INTEROP_PAIRS//,/ }"
FIXTURE="${ROOT}/fixtures/baseline/fixture.json"
DEVICE_ID=$(jq -r '.endDevices[0].lFDI' "${FIXTURE}")
KNOWN_BUGS="${ROOT}/tests/known-bugs.tsv"
require_tools
WORK=$(mktemp -d)
trap 'rm -rf "${WORK}"; cleanup' EXIT
network_up
STARTED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
: > "${WORK}/results.jsonl"

now_ms() {
    if [[ -n "${EPOCHREALTIME:-}" ]]; then local t="${EPOCHREALTIME/[.,]/}"; echo $((t / 1000)); else echo $(($(date +%s) * 1000)); fi
}

# ---- capabilities and upstream versions ----
for adapter in "${ADAPTERS[@]}"; do
    docker run --rm "$(image_of "${adapter}")" print-capabilities > "${WORK}/caps-${adapter}.json" 2>/dev/null \
        || { log "${adapter}: print-capabilities failed"; exit 1; }
    upstream=$(jq -r '.upstream.version' "${WORK}/caps-${adapter}.json")
    log "${adapter}: upstream ${upstream}"
done

caps() { cat "${WORK}/caps-$1.json"; }
upstream_of() { jq -r '.upstream.version' "${WORK}/caps-$1.json"; }
declares() { jq -e --arg v "$2" ".versions[\$v] // {} | $3" "${WORK}/caps-$1.json" >/dev/null 2>&1; }

selected() { [[ ${#PAIRS[@]} -eq 0 || " ${PAIRS[*]} " == *" $1@$2 "* ]]; }

impl() {
    jq -cn --arg n "$1" --arg v "$(upstream_of "$1")" --arg i "$(image_of "$1")" \
        --arg id "$(docker image inspect --format '{{.Id}}' "$(image_of "$1")" 2>/dev/null)" \
        '{name: $n, version: $v, image: $i, imageId: $id}'
}

keep() {
    [[ -n "${REPORT_DIR:-}" && -n "$2" ]] || return 0
    mkdir -p "${REPORT_DIR}"
    jq -e 'type == "object"' <<<"$2" >/dev/null 2>&1 || return 0
    printf '%s\n' "$2" > "${REPORT_DIR}/result-$(echo "$1" | tr -c 'A-Za-z0-9._\n-' '_').json"
}

# record <test id> <client> <server> <initiator> <feature> <operation> <status> <duration ms> [<error or evidence>]
record() {
    local id="$1" client="$2" server="$3" initiator="$4" feat="$5" operation="$6" status="$7" ms="$8" note="${9:-}"
    case "${status}" in
        PASS) ok "${client}->${server}: ${id}" ;;
        FAIL|INFRA_ERROR) fail "${client}->${server}: ${id}: ${note}" ;;
        SKIP_UNSUPPORTED) SKIPPED=$((SKIPPED + 1)) ;;
    esac
    jq -cn --arg id "${id}" --arg v "${CURRENT_VERSION}" --argjson c "$(impl "${client}")" --argjson s "$(impl "${server}")" \
        --arg i "${initiator}" --arg f "${feat}" --arg op "${operation}" --arg st "${status}" --argjson ms "${ms}" \
        --arg err "${note}" '
        {testId: $id, version: $v, client: $c, server: $s, initiator: $i, feature: $f, status: $st, durationMs: $ms}
        + (if $op != "" then {operation: $op} else {} end)
        + (if ($st == "FAIL" or $st == "INFRA_ERROR") and $err != "" then {error: $err} else {} end)' >> "${WORK}/results.jsonl"
}

# Generate TLS material for mTLS tests.
srv_names=()
for a in "${ADAPTERS[@]}"; do srv_names+=("srv-${a}-${RUN_ID}"); done
make_pki "${WORK}/tls/ec" ec "${DEVICE_ID}" "${srv_names[@]}" localhost 127.0.0.1 \
    || { log "cannot generate the TLS material"; exit 1; }
chmod -R a+rX "${WORK}"

CURRENT_VERSION=""

# client_operation <client> <server> <operation> <server container>
client_operation() {
    local client="$1" server="$2" op="$3" srv="$4"
    local id="${CURRENT_VERSION}.${op}" doc code t0 ms
    if ! declares "${client}" "${CURRENT_VERSION}" '.client.supported == true'; then
        record "${id}" "${client}" "${server}" client "${op}" "${op}" SKIP_UNSUPPORTED 0 \
            "${client} does not declare client support for IEEE 2030.5 ${CURRENT_VERSION}"
        return
    fi
    t0=$(now_ms)
    doc=$(run_client "${client}" "${op}" --version "${CURRENT_VERSION}" --url "https://${srv}:443/dcap"); code=$?
    ms=$(( $(now_ms) - t0 ))
    keep "${id}-${client}-${server}" "${doc}"
    if [[ "${code}" == 0 ]] && jq -e '.ok == true' <<<"${doc}" >/dev/null 2>&1; then
        record "${id}" "${client}" "${server}" client "${op}" "${op}" PASS "${ms}"
    else
        record "${id}" "${client}" "${server}" client "${op}" "${op}" FAIL "${ms}" "exit ${code}: ${doc:0:300}"
    fi
}

# mTLS session test: client connects with mTLS, performs a register.
mtls_test() {
    local client="$1" server="$2"
    local srv="srv-${server}-${RUN_ID}" cli_name="cli-${client}-${RUN_ID}" tls="/tls/ec"
    local id="${CURRENT_VERSION}.mtls" t0 ms problems=()
    if ! declares "${client}" "${CURRENT_VERSION}" '.client.supported == true' \
       || ! declares "${server}" "${CURRENT_VERSION}" '.server.supported == true'; then
        record "${id}" "${client}" "${server}" client mtls "" SKIP_UNSUPPORTED 0 \
            "mTLS is not testable: client or server not declared for IEEE 2030.5 ${CURRENT_VERSION}"
        return
    fi
    start "${srv}" "${server}" -v "${WORK}/tls:/tls:ro" -- server --version "${CURRENT_VERSION}" --tls-dir "${tls}/server"
    if ! wait_ready "${srv}"; then
        record "${id}" "${client}" "${server}" client mtls "" INFRA_ERROR 0 "the server did not become ready"
        stop_container "${srv}"; return
    fi
    t0=$(now_ms)
    doc=$(run_client "${client}" -v "${WORK}/tls:/tls:ro" -- register --version "${CURRENT_VERSION}" \
        --url "https://${srv}:443/dcap" --tls-dir "${tls}/client" --connect-timeout-ms 5000); code=$?
    ms=$(( $(now_ms) - t0 ))
    keep "${id}-${client}-${server}" "${doc}"
    if [[ "${code}" == 0 ]] && jq -e '.ok == true' <<<"${doc}" >/dev/null 2>&1; then
        record "${id}" "${client}" "${server}" client mtls register PASS "${ms}"
    else
        record "${id}" "${client}" "${server}" client mtls register FAIL "${ms}" "exit ${code}: ${doc:0:300}"
    fi
    stop_container "${srv}"
}

pairing() {
    local client="$1" server="$2"
    local srv="srv-${server}-${RUN_ID}"
    if ! declares "${client}" "${CURRENT_VERSION}" '.client.supported == true' \
       || ! declares "${server}" "${CURRENT_VERSION}" '.server.supported == true'; then
        record "${CURRENT_VERSION}.pairing" "${client}" "${server}" client other "" SKIP_UNSUPPORTED 0 \
            "IEEE 2030.5 ${CURRENT_VERSION} is not declared by ${client} (client) or ${server} (server)"
        return
    fi
    log "IEEE 2030.5 ${CURRENT_VERSION}: ${client} -> ${server}"

    start "${srv}" "${server}" -- server --version "${CURRENT_VERSION}"
    if ! wait_ready "${srv}"; then
        record "${CURRENT_VERSION}.pairing" "${client}" "${server}" client connectivity "" INFRA_ERROR 0 "the server did not become ready"
        stop_container "${srv}"; return
    fi

    # Run each client operation.
    for op in register discover poll-controls report subscribe; do
        client_operation "${client}" "${server}" "${op}" "${srv}"
    done

    events "${srv}" | grep -v '^{' | grep -q . \
        && record "${CURRENT_VERSION}.events.json-lines" "${client}" "${server}" server other "" FAIL 0 "server stdout has lines that are not JSON"
    stop_container "${srv}"

    # mTLS transport test.
    mtls_test "${client}" "${server}"
}

log "adapters: ${ADAPTERS[*]} (${VERSION}), IEEE 2030.5 ${VERSIONS[*]}${INTEROP_PAIRS:+, server and version: ${INTEROP_PAIRS}}"
for v in "${VERSIONS[@]}"; do
    CURRENT_VERSION="${v}"
    for server in "${ADAPTERS[@]}"; do
        selected "${server}" "${v}" || continue
        for client in "${ADAPTERS[@]}"; do
            pairing "${client}" "${server}"
        done
    done
done

# ---- the report ----
REPORT=$(jq -s --arg id "interop-${RUN_ID}" --arg started "${STARTED}" --arg finished "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg producer "ieee20305-interop scripts/interop.sh (images ${VERSION})" \
    '{schemaVersion: "1.0", run: {id: $id, started: $started, finished: $finished, producer: $producer}, results: .}' \
    "${WORK}/results.jsonl")
if [[ -n "${REPORT_DIR:-}" ]]; then
    mkdir -p "${REPORT_DIR}"
    printf '%s\n' "${REPORT}" > "${REPORT_DIR}/report-interop.json"
fi
log "results by version, pairing and initiator:"
jq -r '.results | group_by([.version, .client.name, .server.name, .initiator])[]
       | "  \(.[0].version) \(.[0].client.name) -> \(.[0].server.name), initiator \(.[0].initiator): "
         + (group_by(.status) | map("\(.[0].status) \(length)") | join(", "))' <<<"${REPORT}" >&2
finish

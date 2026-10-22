#!/usr/bin/env bash
# Reference-stack self-test: every client adapter against every server
# adapter, for every IEEE 2030.5 version both declare.
#
# Usage: interop.sh <adapter> [<adapter> ...]      e.g. interop.sh gridappsd
#
# For each pairing, on a private Docker network, with mutual TLS:
#
#   register, discover, poll-controls, report, subscribe
#                  each operation the client declares for that version
#   unprovisioned  a device of the same CA that the server has not
#                  provisioned is not registered (register is not ok)
#   other-ca       a device certificate of another CA is refused (exit 3)
#
# Every result has the status PASS, FAIL, SKIP_UNSUPPORTED (an operation or
# version one side does not declare, or no TLS cipher suite in common),
# KNOWN_ISSUE (a failure tests/known-bugs.tsv explains) or INFRA_ERROR.
# A known-bug entry whose tests ran and passed fails the run, so the list
# cannot go stale (REQUIREMENTS.md R4.4).
#
# Environment: REGISTRY, VERSION (image tag, default dev), VERBOSE=1,
#              REPORT_DIR (keeps every result document and the report),
#              IEEE20305_VERSIONS (default "2018 2023").

set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
# shellcheck source=scripts/lib.sh
source "${SCRIPT_DIR}/lib.sh"

[[ $# -ge 1 ]] || { echo "usage: interop.sh <adapter> [<adapter> ...]" >&2; exit 2; }
ADAPTERS=("$@")
read -r -a VERSIONS <<<"${IEEE20305_VERSIONS:-2018 2023}"
FIXTURE="${ROOT}/fixtures/baseline/fixture.json"
SERIAL=$(jq -r '.device.serial' "${FIXTURE}")
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

# ---- capabilities ----
for adapter in "${ADAPTERS[@]}"; do
    docker run --rm "$(image_of "${adapter}")" print-capabilities > "${WORK}/caps-${adapter}.json" 2>/dev/null \
        || { log "${adapter}: print-capabilities failed"; exit 1; }
    log "${adapter}: upstream $(jq -r '.upstream.version' "${WORK}/caps-${adapter}.json")"
done
caps() { cat "${WORK}/caps-$1.json"; }
declares() { jq -e --arg v "$2" ".versions[\$v] // {} | $3" "${WORK}/caps-$1.json" >/dev/null 2>&1; }
operations_of() { jq -r --arg v "$2" '.versions[$v].client.operations // [] | .[]' "${WORK}/caps-$1.json"; }
common_ciphers() {
    jq -rn --slurpfile a "${WORK}/caps-$1.json" --slurpfile b "${WORK}/caps-$2.json" \
        '[$a[0].tls.cipherSuites[] as $c | select($b[0].tls.cipherSuites | index($c)) | $c] | join(",")'
}

impl() {
    jq -cn --arg n "$1" --arg v "$(jq -r '.upstream.version' "${WORK}/caps-$1.json")" --arg i "$(image_of "$1")" \
        --arg id "$(docker image inspect --format '{{.Id}}' "$(image_of "$1")" 2>/dev/null)" \
        '{name: $n, version: $v, image: $i, imageId: $id}'
}

keep() {
    [[ -n "${REPORT_DIR:-}" && -n "$2" ]] || return 0
    mkdir -p "${REPORT_DIR}"
    jq -e 'type == "object"' <<<"$2" >/dev/null 2>&1 || return 0
    printf '%s\n' "$2" > "${REPORT_DIR}/result-$(echo "$1" | tr -c 'A-Za-z0-9._\n-' '_').json"
}

# ---- known upstream bugs ----
# known_bug <client> <server> <test id>: prints "<line number>\t<evidence>"
# of the first entry of tests/known-bugs.tsv that explains a failure.
# An entry is adapter[:role][@peer], upstream version, test id regex,
# evidence URL, summary.
known_bug() {
    local client="$1" server="$2" id="$3" n=0 who version regex evidence adapter role peer
    # The fifth column, the summary, is for people.
    while IFS=$'\t' read -r who version regex evidence _; do
        n=$((n + 1))
        [[ -z "${who}" || "${who}" == \#* ]] && continue
        adapter="${who%%[:@]*}"; role=""; peer=""
        [[ "${who}" == *:* ]] && { role="${who#*:}"; role="${role%%@*}"; }
        [[ "${who}" == *@* ]] && peer="${who#*@}"
        local self other
        case "${role}" in
            client) self="${client}"; other="${server}" ;;
            server) self="${server}"; other="${client}" ;;
            *) if [[ "${adapter}" == "${client}" ]]; then self="${client}"; other="${server}"; else self="${server}"; other="${client}"; fi ;;
        esac
        [[ "${self}" == "${adapter}" ]] || continue
        [[ -z "${peer}" || "${peer}" == "${other}" ]] || continue
        [[ "$(jq -r '.upstream.version' "${WORK}/caps-${adapter}.json")" == "${version}" ]] || continue
        [[ "${id}" =~ ${regex} ]] || continue
        printf '%s\t%s\n' "${n}" "${evidence}"; return 0
    done < "${KNOWN_BUGS}"
    return 1
}
: > "${WORK}/known-hit"
: > "${WORK}/known-ran"

# record <test id> <client> <server> <operation> <status> <duration ms> [<error>]
record() {
    local id="$1" client="$2" server="$3" operation="$4" status="$5" ms="$6" note="${7:-}" bug
    if bug=$(known_bug "${client}" "${server}" "${id}"); then
        echo "${bug%%$'\t'*}" >> "${WORK}/known-ran"
        if [[ "${status}" == FAIL ]]; then
            status=KNOWN_ISSUE; note="${note} (known: ${bug#*$'\t'})"
            echo "${bug%%$'\t'*}" >> "${WORK}/known-hit"
        fi
    fi
    case "${status}" in
        PASS) ok "${client}->${server}: ${id}" ;;
        FAIL|INFRA_ERROR) fail "${client}->${server}: ${id}: ${note}" ;;
        KNOWN_ISSUE) KNOWN=$((KNOWN + 1)) ;;
        SKIP_UNSUPPORTED) SKIPPED=$((SKIPPED + 1)); [[ -n "${VERBOSE:-}" ]] && log "skip ${client}->${server}: ${id}: ${note}" ;;
    esac
    jq -cn --arg id "${id}" --arg v "${CURRENT_VERSION}" --argjson c "$(impl "${client}")" --argjson s "$(impl "${server}")" \
        --arg op "${operation}" --arg st "${status}" --argjson ms "${ms}" --arg note "${note}" '
        {testId: $id, version: $v, client: $c, server: $s, status: $st, durationMs: $ms}
        + (if $op != "" then {operation: $op} else {} end)
        + (if $note != "" and $st != "PASS" then {note: $note} else {} end)' >> "${WORK}/results.jsonl"
}

# ---- the throwaway PKI: one server certificate for every server name ----
srv_names=()
for a in "${ADAPTERS[@]}"; do srv_names+=("srv-${a}-${RUN_ID}"); done
make_pki "${WORK}/pki" "${SERIAL}" "${srv_names[@]}" localhost || { log "cannot generate the TLS material"; exit 1; }
TLS=(-v "${WORK}/pki:/tls:ro")

# run_op <client> <server> <test id> <operation> <tls dir> <server container>:
# prints the result document, returns the exit code.
run_op() {
    run_client "$1" "${TLS[@]}" -- "$4" --version "${CURRENT_VERSION}" --url "https://$6:443/dcap" --tls-dir "/tls/$5" --connect-timeout-ms 5000
}

pairing() {
    local client="$1" server="$2"
    local srv="srv-${server}-${RUN_ID}" ciphers op doc code t0 id
    if ! declares "${client}" "${CURRENT_VERSION}" '.client.supported == true' \
       || ! declares "${server}" "${CURRENT_VERSION}" '.server.supported == true'; then
        return
    fi
    ciphers=$(common_ciphers "${client}" "${server}")
    if [[ -z "${ciphers}" ]]; then
        record "${CURRENT_VERSION}.tls" "${client}" "${server}" "" SKIP_UNSUPPORTED 0 \
            "no TLS cipher suite in common: ${client} offers $(caps "${client}" | jq -r '.tls.cipherSuites | join(",")'), ${server} $(caps "${server}" | jq -r '.tls.cipherSuites | join(",")')"
        return
    fi
    log "IEEE 2030.5 ${CURRENT_VERSION}: ${client} -> ${server} (${ciphers})"
    start "${srv}" "${server}" "${TLS[@]}" -- server --version "${CURRENT_VERSION}" --tls-dir /tls/server
    if ! wait_ready "${srv}"; then
        record "${CURRENT_VERSION}.ready" "${client}" "${server}" "" INFRA_ERROR 0 "the server did not become ready"
        stop_container "${srv}"; return
    fi

    for op in register discover poll-controls report subscribe; do
        id="${CURRENT_VERSION}.${op}"
        if ! operations_of "${client}" "${CURRENT_VERSION}" | grep -qx "${op}"; then
            record "${id}" "${client}" "${server}" "${op}" SKIP_UNSUPPORTED 0 "${client} does not declare the client operation ${op}"; continue
        fi
        if ! jq -e --arg v "${CURRENT_VERSION}" --arg op "${op}" '.versions[$v].server.operations | index($op)' "${WORK}/caps-${server}.json" >/dev/null 2>&1; then
            record "${id}" "${client}" "${server}" "${op}" SKIP_UNSUPPORTED 0 "${server} does not declare the server operation ${op}"; continue
        fi
        t0=$(now_ms)
        doc=$(run_op "${client}" "${server}" "${id}" "${op}" client "${srv}"); code=$?
        keep "${id}-${client}-${server}" "${doc}"
        if [[ "${code}" == 0 ]] && jq -e '.ok == true' <<<"${doc}" >/dev/null 2>&1
        then record "${id}" "${client}" "${server}" "${op}" PASS $(( $(now_ms) - t0 ))
        else record "${id}" "${client}" "${server}" "${op}" FAIL $(( $(now_ms) - t0 )) "exit ${code}: ${doc:0:300}"; fi
    done

    if operations_of "${client}" "${CURRENT_VERSION}" | grep -qx register; then
        id="${CURRENT_VERSION}.register.unprovisioned"; t0=$(now_ms)
        doc=$(run_op "${client}" "${server}" "${id}" register unprovisioned "${srv}"); code=$?
        keep "${id}-${client}-${server}" "${doc}"
        if [[ "${code}" != 0 ]] && jq -e '.ok == false' <<<"${doc}" >/dev/null 2>&1
        then record "${id}" "${client}" "${server}" register PASS $(( $(now_ms) - t0 ))
        else record "${id}" "${client}" "${server}" register FAIL $(( $(now_ms) - t0 )) "an unprovisioned device was registered: exit ${code}: ${doc:0:300}"; fi
    fi

    id="${CURRENT_VERSION}.mtls.other-ca"; t0=$(now_ms)
    doc=$(run_op "${client}" "${server}" "${id}" discover other-ca "${srv}"); code=$?
    if [[ "${code}" == 3 ]] && jq -e '.error.code == "connect-failed"' <<<"${doc}" >/dev/null 2>&1
    then record "${id}" "${client}" "${server}" discover PASS $(( $(now_ms) - t0 ))
    else record "${id}" "${client}" "${server}" discover FAIL $(( $(now_ms) - t0 )) "a device of another CA was not refused: exit ${code}: ${doc:0:300}"; fi

    if events "${srv}" | grep -v '^{' | grep -q .; then
        record "${CURRENT_VERSION}.events.json-lines" "${client}" "${server}" "" FAIL 0 "server stdout has lines that are not JSON"
    fi
    stop_container "${srv}"
}

CURRENT_VERSION=""
log "adapters: ${ADAPTERS[*]} (${VERSION}), IEEE 2030.5 ${VERSIONS[*]}"
for v in "${VERSIONS[@]}"; do
    CURRENT_VERSION="${v}"
    for server in "${ADAPTERS[@]}"; do
        for client in "${ADAPTERS[@]}"; do
            pairing "${client}" "${server}"
        done
    done
done

# ---- stale known-bug entries: their tests ran and none failed ----
while read -r line; do
    [[ -n "${line}" ]] || continue
    if ! grep -qx "${line}" "${WORK}/known-hit"; then
        fail "tests/known-bugs.tsv line ${line} no longer reproduces: remove it"
    fi
done < <(sort -u "${WORK}/known-ran")

# ---- the report ----
REPORT=$(jq -s --arg id "interop-${RUN_ID}" --arg started "${STARTED}" --arg finished "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg producer "ieee20305-interop scripts/interop.sh (images ${VERSION})" \
    '{schemaVersion: "1.0", run: {id: $id, started: $started, finished: $finished, producer: $producer}, results: .}' \
    "${WORK}/results.jsonl")
if [[ -n "${REPORT_DIR:-}" ]]; then
    mkdir -p "${REPORT_DIR}"
    printf '%s\n' "${REPORT}" > "${REPORT_DIR}/report-interop.json"
fi
log "results by version and pairing:"
jq -r '.results | group_by([.version, .client.name, .server.name])[]
       | "  \(.[0].version) \(.[0].client.name) -> \(.[0].server.name): "
         + (group_by(.status) | map("\(.[0].status) \(length)") | join(", "))' <<<"${REPORT}" >&2
finish

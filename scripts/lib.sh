#!/usr/bin/env bash
# Shared helpers for the self-test scripts. Source, do not execute.
#
# Servers and clients run as containers on a private Docker network and reach
# each other by container name, so no host port is published and parallel runs
# do not collide.

REGISTRY="${REGISTRY:-ghcr.io/otfabric}"
VERSION="${VERSION:-dev}"
RUN_ID="${RUN_ID:-$$}"
NETWORK="ieee20305-interop-${RUN_ID}"
READY_FILE="/run/ieee20305-interop/ready"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-60}"

PASS=0
FAIL=0
KNOWN=0
SKIPPED=0

# shellcheck source=scripts/results.sh
source "$(dirname "${BASH_SOURCE[0]}")/results.sh"

log()  { echo "[$(basename "$0" .sh)] $*" >&2; }
ok()   { PASS=$((PASS + 1)); [[ -n "${VERBOSE:-}" ]] && log "ok: ${CHECK_PREFIX:-}$*"; return 0; }
fail() { FAIL=$((FAIL + 1)); log "FAIL: ${CHECK_PREFIX:-}$*"; return 0; }

image_of() { echo "${REGISTRY}/ieee20305-interop-$1:${VERSION}"; }

require_tools() {
    local t
    for t in docker jq; do
        command -v "$t" >/dev/null || { log "missing required tool: $t"; exit 2; }
    done
}

network_up() { docker network create "${NETWORK}" >/dev/null; }

cleanup() {
    docker ps -aq --filter "label=ieee20305-interop.run=${RUN_ID}" | xargs -r docker rm -f >/dev/null 2>&1
    docker network rm "${NETWORK}" >/dev/null 2>&1
    return 0
}

# start <name> <adapter> [docker run options --] <command and args...>
# Starts a detached container on the test network.
start() {
    local name="$1" adapter="$2"; shift 2
    local opts=()
    if [[ " $* " == *" -- "* ]]; then
        while [[ "$1" != "--" ]]; do opts+=("$1"); shift; done
        shift
    fi
    docker rm -f "${name}" >/dev/null 2>&1
    docker run -d --name "${name}" --network "${NETWORK}" --label "ieee20305-interop.run=${RUN_ID}" \
        ${opts[@]+"${opts[@]}"} "$(image_of "${adapter}")" "$@" >/dev/null
}

# wait_ready <container>: the ready file exists only once the server listens.
# No fixed delays.
wait_ready() {
    local name="$1" waited=0 state
    while true; do
        state=$(docker inspect --format '{{.State.Status}}' "${name}" 2>/dev/null || echo absent)
        if [[ "${state}" != "running" ]]; then
            log "${name} is ${state}"; docker logs --tail 20 "${name}" >&2 2>/dev/null; return 1
        fi
        docker exec "${name}" test -f "${READY_FILE}" 2>/dev/null && return 0
        if (( waited >= WAIT_TIMEOUT * 5 )); then
            log "${name} not ready after ${WAIT_TIMEOUT}s"; docker logs --tail 20 "${name}" >&2; return 1
        fi
        sleep 0.2; waited=$((waited + 1))
    done
}

stop_container() { docker rm -f "$1" >/dev/null 2>&1; return 0; }

# The scripts run with pipefail. A reader that stops early (head -1) makes
# the writer die of SIGPIPE when it is still writing, and pipefail then
# fails the whole pipeline although the reader got its line: take the first
# line with sed -n 1p, which reads to the end.

# events <container>: the JSON Lines a container wrote to stdout.
events() { docker logs "$1" 2>/dev/null; }

# wait_event <container> <jq condition> [args for jq...]: prints the first
# event that satisfies the condition, waiting up to 5 s for it.
wait_event() {
    local name="$1" condition="$2"; shift 2
    local i found
    for i in $(seq 1 50); do
        found=$(events "${name}" | jq -c "$@" "select(${condition})" 2>/dev/null | sed -n 1p)
        [[ -n "${found}" ]] && { echo "${found}"; return 0; }
        (( i < 50 )) && sleep 0.1
    done
    return 1
}

# exited <container>: waits (up to 5 s) until the stopped event, the last
# line an adapter writes, has reached the container's log. Docker can report
# a container as exited before its log copier has written the last lines, so
# reading the log right after the exit may miss them.
exited() { wait_event "$1" '.event == "stopped"' >/dev/null || true; }

# run_client <adapter> [docker run options --] <client args...>: prints the
# result document of a one-shot client and returns the adapter's exit code.
run_client() {
    local adapter="$1"; shift
    local opts=()
    if [[ " $* " == *" -- "* ]]; then
        while [[ "$1" != "--" ]]; do opts+=("$1"); shift; done
        shift
    fi
    docker run --rm --network "${NETWORK}" --label "ieee20305-interop.run=${RUN_ID}" \
        ${opts[@]+"${opts[@]}"} "$(image_of "${adapter}")" client "$@" 2>/dev/null
}

# JSON helpers for jq, shared by the tests:
#   norm   date-times without a zero fraction of a second, so that
#          2026-01-01T10:00:00.000Z and 2026-01-01T10:00:00Z compare equal
#   within(want; got)  every field of want is in got with an equal value
#          (numbers compare by value); "${now}" in want stands for any RFC 3339
#          UTC date-time. Arrays must have the same length.
# shellcheck disable=SC2016,SC2034
JQ_LIB='
def isdate: type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$");
def norm: if isdate then sub("\\.0+(?=Z$)"; "") | sub("\\+00:00$"; "Z") else . end;
def within($want; $got):
  if ($want | type) == "object" then
    ($got | type) == "object" and ([$want | to_entries[] | . as $e | ($got | has($e.key)) and within($e.value; $got[$e.key])] | all)
  elif ($want | type) == "array" then
    ($got | type) == "array" and ($want | length) == ($got | length)
    and ([range(0; $want | length) as $i | within($want[$i]; $got[$i])] | all)
  elif $want == "${now}" then ($got | isdate)
  elif ($want | type) == "string" then ($got | type) == "string" and ($want | norm) == ($got | norm)
  else $want == $got end;
'

# ---- transport security: a throwaway PKI, generated at test time ----
# Nothing of it is committed or baked into an image (REQUIREMENTS.md R7.3).
# IEEE 2030.5 requires mutual TLS with ECDSA P-256 certificates. The
# certificates follow the IEEE 2030.5 and CSIP certificate profile as far as
# the reference stacks check it:
#   - device certificates carry a critical subjectAltName holding only an
#     RFC 4108 HardwareModuleName, and the IEEE 2030.5 policy OIDs;
#   - server certificates carry a critical subjectAltName with their host
#     names, a critical keyUsage, and both serverAuth and clientAuth (some
#     clients verify the server with the clientAuth usage, as CSIP servers
#     carry both).

# IEEE 2030.5 certificate policy OIDs (IEEE 2030.5 section 6.11.8.4): device
# certificate (generic), test certificate.
IEEE20305_POLICY_DEVICE="1.3.6.1.4.1.40732.1.1"
IEEE20305_POLICY_TEST="1.3.6.1.4.1.40732.2.2"

# new_key <file>: an unencrypted PKCS #8 PEM key on P-256.
new_key() {
    openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$1" 2>/dev/null
}

# new_ca <dir> <name>: <dir>/ca.key and the self-signed <dir>/ca.crt.
new_ca() {
    local dir="$1"
    mkdir -p "${dir}"
    new_key "${dir}/ca.key" || return 1
    cat > "${dir}/ca.cnf" <<CNF
[req]
distinguished_name = dn
prompt = no
x509_extensions = ca
[dn]
CN = $2
[ca]
basicConstraints = critical, CA:TRUE
keyUsage = critical, keyCertSign, cRLSign
subjectKeyIdentifier = hash
CNF
    openssl req -x509 -new -key "${dir}/ca.key" -config "${dir}/ca.cnf" -days 2 -sha256 -out "${dir}/ca.crt" 2>/dev/null
}

# sign <ca dir> <out prefix> <subject> <extensions file>: signs a new key's
# request with the CA into <out prefix>.crt, with <out prefix>.key.
sign() {
    local ca="$1" out="$2" subject="$3" ext="$4" status=0
    new_key "${out}.key" || return 1
    openssl req -new -key "${out}.key" -subj "${subject}" -out "${out}.csr" 2>/dev/null || return 1
    openssl x509 -req -in "${out}.csr" -CA "${ca}/ca.crt" -CAkey "${ca}/ca.key" -CAcreateserial \
        -days 2 -sha256 -extfile "${ext}" -extensions leaf -out "${out}.crt" 2>"${out}.err" || status=$?
    [[ "${status}" == 0 ]] || log "openssl x509: $(cat "${out}.err")"
    rm -f "${out}.csr" "${out}.err" "${ext}"
    return "${status}"
}

# new_server <ca dir> <out prefix> <host names or IP addresses...>
new_server() {
    local ca="$1" out="$2" name san=""; shift 2
    for name in "$@"; do
        if [[ "${name}" =~ ^[0-9.]+$ ]]; then san="${san:+${san}, }IP:${name}"; else san="${san:+${san}, }DNS:${name}"; fi
    done
    cat > "${out}.ext" <<EXT
[leaf]
basicConstraints = critical, CA:FALSE
keyUsage = critical, digitalSignature, keyAgreement
extendedKeyUsage = serverAuth, clientAuth
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid
subjectAltName = critical, ${san}
EXT
    sign "${ca}" "${out}" "/CN=$1" "${out}.ext"
}

# new_device <ca dir> <out prefix> <serial number>: a device certificate
# whose HardwareModuleName carries the serial number.
new_device() {
    local ca="$1" out="$2" serial="$3" hex
    hex=$(printf '%s' "${serial}" | od -An -tx1 | tr -d ' \n')
    cat > "${out}.ext" <<EXT
[leaf]
basicConstraints = critical, CA:FALSE
keyUsage = critical, digitalSignature, keyAgreement
extendedKeyUsage = clientAuth
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid
certificatePolicies = ${IEEE20305_POLICY_DEVICE}, ${IEEE20305_POLICY_TEST}
subjectAltName = critical, otherName:1.3.6.1.5.5.7.8.4;SEQUENCE:hmn
[hmn]
hwType = OID:${IEEE20305_POLICY_DEVICE}
hwSerialNum = FORMAT:HEX,OCTETSTRING:${hex}
EXT
    sign "${ca}" "${out}" "/CN=${serial}" "${out}.ext"
}

# lfdi_of <cert file>: the LFDI (Long Form Device Identifier), the leftmost
# 160 bits of the SHA-256 fingerprint of the DER certificate, as 40
# upper-case hexadecimal digits (IEEE 2030.5 section 6.3.4).
lfdi_of() {
    openssl x509 -in "$1" -outform DER 2>/dev/null | openssl dgst -sha256 -hex 2>/dev/null \
        | awk '{print toupper(substr($NF, 1, 40))}'
}

# sfdi_of <cert file>: the SFDI (Short Form Device Identifier), the leftmost
# 36 bits of the fingerprint as a decimal number followed by a check digit
# that makes the sum of all digits a multiple of 10 (IEEE 2030.5 section 6.3.3).
sfdi_of() {
    local hex value sum=0 i
    hex=$(lfdi_of "$1" | cut -c1-9)
    [[ -n "${hex}" ]] || return 1
    value=$((16#${hex}))
    for ((i = 0; i < ${#value}; i++)); do sum=$((sum + ${value:i:1})); done
    echo "${value}$(( (10 - sum % 10) % 10 ))"
}

# make_pki <dir> <device serial> <server host names...> [-- <client host names...>]:
# the --tls-dir directories for IEEE 2030.5 mutual TLS:
#   server/          server.crt (for every host name given), server.key,
#                    ca.crt, and devices/<serial>.crt: the device
#                    certificates the server provisions
#   client/          client.crt and client.key of the provisioned device,
#                    and ca.crt; with client host names, notify.crt and
#                    notify.key, the certificate of the client's
#                    notification listener for those names (subscribe)
#   unprovisioned/   as client/, for a device of the same CA that the server
#                    has not provisioned
#   other-ca/        as client/, with a device certificate from another CA
#   untrusted/       the client's certificate, and the ca.crt of another CA
#                    (the server's certificate does not verify)
make_pki() {
    local dir="$1" serial="$2" d; shift 2
    local servers=() clients=()
    while [[ $# -gt 0 && "$1" != "--" ]]; do servers+=("$1"); shift; done
    [[ "${1:-}" == "--" ]] && { shift; clients=("$@"); }
    command -v openssl >/dev/null || { log "missing required tool: openssl"; return 1; }
    mkdir -p "${dir}"
    new_ca "${dir}/.ca" "ieee20305-interop test CA ${RUN_ID}" || return 1
    new_ca "${dir}/.rogue" "ieee20305-interop other CA ${RUN_ID}" || return 1
    for d in server/devices client unprovisioned other-ca untrusted; do mkdir -p "${dir}/${d}"; done
    new_server "${dir}/.ca" "${dir}/server/server" "${servers[@]}" || return 1
    new_device "${dir}/.ca" "${dir}/client/client" "${serial}" || return 1
    if [[ ${#clients[@]} -gt 0 ]]; then
        new_server "${dir}/.ca" "${dir}/client/notify" "${clients[@]}" || return 1
    fi
    new_device "${dir}/.ca" "${dir}/unprovisioned/client" "unprovisioned-${serial}" || return 1
    new_device "${dir}/.rogue" "${dir}/other-ca/client" "${serial}" || return 1
    cp "${dir}/client/client.crt" "${dir}/server/devices/${serial}.crt"
    for d in server client unprovisioned other-ca; do cp "${dir}/.ca/ca.crt" "${dir}/${d}/ca.crt"; done
    cp "${dir}/.rogue/ca.crt" "${dir}/untrusted/ca.crt"
    cp "${dir}/client/client.crt" "${dir}/client/client.key" "${dir}/untrusted/"
    rm -rf "${dir}/.ca" "${dir}/.rogue"
    # The containers run as uid 10001 and mount the directories read-only.
    chmod -R a+rX "${dir}"
}

# NOTIFY_PORT is the port of the client's notification listener in the
# subscribe tests.
NOTIFY_PORT=8443

# run_subscribe <client adapter> <client host name> <server container>
#   <trigger: yes or no> [docker run options --] <client args...>:
# runs client subscribe in a container named and reachable as <client host
# name>, waits until the server reports subscription-created, then (with
# trigger yes) makes the server change what it serves with call
# issue-control, so that it notifies; prints the result document and
# returns the client's exit code. No server notifies a new subscription by
# itself (docs/CONTAINER_CONTRACT.md).
run_subscribe() {
    local adapter="$1" name="$2" srv="$3" trigger="$4"; shift 4
    local opts=() i state code before
    if [[ " $* " == *" -- "* ]]; then
        while [[ "$1" != "--" ]]; do opts+=("$1"); shift; done
        shift
    fi
    subscriptions() { events "${srv}" | jq -c 'select(.event == "subscription-created")' 2>/dev/null | wc -l; }
    before=$(subscriptions)
    docker rm -f "${name}" >/dev/null 2>&1
    docker run -d --name "${name}" --hostname "${name}" --network "${NETWORK}" --label "ieee20305-interop.run=${RUN_ID}" \
        ${opts[@]+"${opts[@]}"} "$(image_of "${adapter}")" client subscribe "$@" \
        --notify-listen "0.0.0.0:${NOTIFY_PORT}" --notify-host "${name}:${NOTIFY_PORT}" >/dev/null || return 1
    for i in $(seq 1 300); do
        state=$(docker inspect --format '{{.State.Status}}' "${name}" 2>/dev/null || echo absent)
        [[ "${state}" == running ]] || break
        if (( $(subscriptions) > before )); then
            if [[ "${trigger}" == yes ]]; then
                docker exec "${srv}" ieee20305-interop call issue-control \
                    --payload '{"description": "Subscription test", "base": {"opModEnergize": true}, "duration": 600, "start": 0}' >/dev/null 2>&1
            fi
            break
        fi
        sleep 0.1
    done
    code=$(docker wait "${name}" 2>/dev/null)
    docker logs "${name}" 2>/dev/null
    docker rm -f "${name}" >/dev/null 2>&1
    return "${code:-1}"
}

finish() {
    echo "" >&2
    local extra=""
    [[ "${KNOWN}" -gt 0 ]] && extra="${extra}, ${KNOWN} known upstream bugs"
    [[ "${SKIPPED}" -gt 0 ]] && extra="${extra}, ${SKIPPED} skipped (capability not declared)"
    log "${PASS} passed, ${FAIL} failed${extra}"
    results_close || FAIL=$((FAIL + 1))
    [[ "${FAIL}" -eq 0 && "${PASS}" -gt 0 ]]
}

KNOWN_BUGS="${ROOT}/tests/known-bugs.tsv"

# ---- known upstream bugs (tests/known-bugs.tsv) ----
# Needs ${WORK}/caps-<adapter>.json, the capability document of each adapter.
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

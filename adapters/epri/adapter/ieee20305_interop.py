# SPDX-License-Identifier: MIT
"""ieee20305-interop adapter for the EPRI IEEE 2030.5 client.

Runs EPRI's client_test, unmodified but for the patches of adapters/epri/
patches, for each one-shot client operation, and turns what it prints into
the contract's result document. client_test prints the HTTP exchanges and
every document it parsed, runs until it is stopped, and exits 0 whatever
happens; so the adapter reads its output, decides when the operation is
complete, and stops it.

EPRI's client is a client only: the server command validates its flags and
exits 5. Standard library only: the image carries Debian's python3.
"""

from __future__ import annotations

import base64
import json
import os
import queue
import re
import socket
import subprocess
import sys
import threading
import time
import xml.etree.ElementTree as ET
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

EXIT_OK, EXIT_FAILED, EXIT_USAGE, EXIT_CONNECT, EXIT_FIXTURE, EXIT_UNSUPPORTED = 0, 1, 2, 3, 4, 5

ADAPTER = "epri"
CONTRACT_VERSION = "0.3"
EDITIONS = ("2018", "2023")  # what --version accepts
CLIENT_EDITIONS = ("2018",)  # what EPRI's client is run against
# The contract's one-shot client operations.
CONTRACT_OPERATIONS = ("register", "discover", "poll-controls", "report", "subscribe", "respond", "log-event",
                       "put-der", "flow-reservation", "poll-pricing")
# What the adapter performs with client_test, and declares: an operation it
# performs is declared, and the failures that upstream defects cause are in
# tests/known-bugs.tsv.
PERFORMED = ("register", "discover", "poll-controls", "report", "respond")
OPERATIONS = PERFORMED
# The baked fixtures the client declares: those the self-tests pass with.
# register and discover use nothing of a fixture but its PIN, so another
# fixture with the PIN 111115 works as well, once a server is seeded with it.
CLIENT_FIXTURES = ("baseline",)
FIXTURES = Path("/fixtures")
DEFAULT_FIXTURE = FIXTURES / "baseline" / "fixture.json"
EPRI = Path("/opt/epri")
WORK = Path("/tmp/ieee20305-interop")

ADAPTER_VERSION = os.environ.get("IEEE20305_INTEROP_ADAPTER_VERSION", "dev")
UPSTREAM_VERSION = os.environ.get("IEEE20305_INTEROP_UPSTREAM_VERSION", "unknown")
UPSTREAM_REVISION = os.environ.get("IEEE20305_INTEROP_UPSTREAM_REVISION", "unknown")

# client_test's own command for each operation; discover is a plain
# retrieval of the URL.
# report: `fsa meter` reads the EndDevice and its FunctionSetAssignments
# and DER programs (the EndDevice is complete only then) without the DER
# controls, and posts client_test's own MirrorUsagePoint. respond: `all`
# runs the schedule test, which responds to the controls.
COMMANDS = {"discover": [], "register": ["inverter", "register"], "poll-controls": ["inverter", "all"],
            "report": ["inverter", "fsa", "meter"], "respond": ["inverter", "all"]}


class UsageError(Exception):
    """A malformed invocation: exit 2."""


class CodedError(Exception):
    """Ends the command with a contract exit code."""

    def __init__(self, code: int, message: str) -> None:
        super().__init__(message)
        self.code = code


class OperationError(Exception):
    """A failed operation, with the result document's error code."""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code


def main(argv: list[str]) -> int:
    if not argv:
        print("usage: ieee20305-interop server|client|call|print-capabilities|print-fixture|version", file=sys.stderr)
        return EXIT_USAGE
    command, args = argv[0], argv[1:]
    try:
        if command == "client":
            return run_client(args)
        if command == "server":
            flags = parse_flags(args, "version", "port", "tls-dir", "fixture", "ready-file", "control-socket")
            edition_of(flags)
            required(flags, "tls-dir")
            raise CodedError(EXIT_UNSUPPORTED, "the EPRI client has no server role")
        if command == "call":
            return run_call(args)
        if command == "print-capabilities":
            if args:
                raise UsageError("print-capabilities takes no arguments")
            emit(capabilities())
            return EXIT_OK
        if command == "print-fixture":
            return print_fixture(args)
        if command == "version":
            if args:
                raise UsageError("version takes no arguments")
            print(f"ieee20305-interop {ADAPTER} {ADAPTER_VERSION} (EPRI client {UPSTREAM_VERSION})")
            return EXIT_OK
        raise UsageError(f"unknown command {command!r}")
    except UsageError as exc:
        print(f"ieee20305-interop: {exc}", file=sys.stderr)
        return EXIT_USAGE
    except CodedError as exc:
        print(f"ieee20305-interop: {exc}", file=sys.stderr)
        return exc.code


def emit(document: dict[str, Any]) -> None:
    """Writes one JSON line to stdout."""
    sys.stdout.write(json.dumps(document, separators=(",", ":")) + "\n")
    sys.stdout.flush()


# ---- flags ----


def parse_flags(args: list[str], *allowed: str) -> dict[str, str]:
    """--name value, each at most once, none unknown."""
    flags: dict[str, str] = {}
    i = 0
    while i < len(args):
        name = args[i][2:] if args[i].startswith("--") else ""
        if not name:
            raise UsageError(f"unexpected argument {args[i]!r}")
        if name not in allowed:
            raise UsageError(f"unknown flag --{name}")
        if name in flags:
            raise UsageError(f"flag --{name} given twice")
        if i + 1 >= len(args):
            raise UsageError(f"flag --{name} needs a value")
        flags[name] = args[i + 1]
        i += 2
    return flags


def required(flags: dict[str, str], name: str) -> str:
    if name not in flags:
        raise UsageError(f"--{name} is required")
    return flags[name]


def edition_of(flags: dict[str, str]) -> str:
    edition = required(flags, "version")
    if edition not in EDITIONS:
        raise UsageError(f"--version {edition!r} is not an IEEE 2030.5 version: one of {', '.join(EDITIONS)}")
    return edition


def millis(flags: dict[str, str], name: str, default: float) -> float:
    if name not in flags:
        return default
    try:
        value = int(flags[name])
    except ValueError:
        value = 0
    if value <= 0:
        raise UsageError(f"--{name} {flags[name]!r} is not a positive number of milliseconds")
    return value / 1000


# ---- capabilities and fixtures ----


def capabilities() -> dict[str, Any]:
    pin = "https://github.com/epri-dev/IEEE-2030.5-Client/blob/505ee47d9c3ed43ce63087198d09f77338da627d"
    notes = [
        "EPRI's client_test (v0.2.11, unmaintained since 2018), built with its own build script against "
        "Debian trixie's OpenSSL 3, with one patch: the SHA-256 padding of its LFDI computation, which crashed "
        "for one device certificate in eight (adapters/epri/patches).",
        "TLS: TLS 1.2 with TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8 only, the client's own cipher list; OpenSSL runs "
        "at security level 0 (adapters/epri/openssl.cnf), under which OpenSSL 3 offers CCM_8 as OpenSSL 1.1.0 did.",
        "Schema: EPRI's sep.xsd 2.0.4, a draft of IEEE 2030.5-2018. Its parser accepts no element outside that "
        "schema, such as the CSIP-AUS element csipaus:ConnectionPointLink that Envoy puts in EndDevice "
        f"({pin}/parse.c#L215), so against Envoy register fails (tests/known-bugs.tsv).",
        "Everything client_test sends (POST, PUT) is in the draft name space http://ieee.org/2030.5 "
        f"({pin}/se_schema.c#L2235), not urn:ieee:std:2030.5:ns of IEEE 2030.5-2018; GridAPPS-D refuses such "
        "a body with 400 (expected element in name space urn:ieee:std:2030.5:ns).",
        "Connects by IP address (the adapter resolves the URL's host name) and does not check the server's host "
        "name against its certificate.",
        "register checks for the PIN 111115, which client_test hard-codes "
        f"({pin}/client_test.c#L327); the adapter checks the fixture's PIN is that. register and discover use "
        "nothing else of the fixture; the client declares the baseline fixture, the only one a server it "
        "works with (GridAPPS-D) is seeded with in the self-tests.",
        "poll-controls runs inverter all. Known upstream issues (tests/known-bugs.tsv): client_test crashes on "
        "GridAPPS-D's DERControlList href, longer than its char uri[64] "
        f"({pin}/retrieve.c#L140), and cannot parse Envoy's EndDevice. It matches programs and controls only, "
        "not default controls or curves, so a fixture with those is unsupported; and client_test prints a "
        "present optional boolean as true whatever its value, so it matches boolean control elements by "
        "presence only.",
        "report runs inverter fsa meter: client_test posts a MirrorUsagePoint of its own (a random mRID, "
        "roleFlags 09; then seven readings W, VA, var, PF, V, A, Hz with hard-coded values), not the fixture's "
        f"({pin}/client_test.c#L250-L265), once it has read the EndDevice's FunctionSetAssignments and DER "
        "programs; ok on 201 with a Location from the MirrorUsagePointList. Known upstream issues: GridAPPS-D "
        "refuses its draft name space (400 on POST /mup), and Envoy's EndDevice does not parse.",
        "respond runs inverter all: client_test's schedule test posts DERControlResponses to the replyTo of "
        f"a control ({pin}/schedule.c#L142-L151), Received (1) when responseRequired has bit 0 and Started (2) "
        "and the others when it has bit 1; ok once one for the fixture's control active now (start 0) is "
        "accepted (201 or 204); response.body lists the statuses it sent. Known upstream issues: the crash on "
        "GridAPPS-D's DERControlList href (retrieve.c#L140), the draft name space, and Envoy's EndDevice.",
        "log-event is not offered: client_test's alarm test posts its own LogEvent (details \"Alarms Test\", "
        f"PEN 37244, no functionSet; {pin}/client_test.c#L188-L208), not the fixture's, after reading the DER "
        "controls, which crashes on GridAPPS-D (retrieve.c#L140).",
        "put-der is not offered: client_test PUTs the DERAvailability, DERCapability, DERSettings and DERStatus "
        f"of its ./settings files (draft 2.0.4 elements), not the fixture's, to the first DER of the EndDevice's "
        f"DERList ({pin}/client_test.c#L214-L231); GridAPPS-D's EndDevice has no DERListLink.",
        f"subscribe is not offered: client_test's subscription code is commented out ({pin}/client_test.c#L235-L239). "
        "flow-reservation and poll-pricing are not in client_test. client run and call are not implemented.",
    ]
    client = {"supported": True, "operations": list(OPERATIONS), "fixtures": list(CLIENT_FIXTURES)}
    server = {"supported": False, "operations": []}
    features = {"mTLS": True, "csip": False, "csipAus": False, "subscription": False, "defaultControl": False,
                "curves": False, "pricing": False, "messaging": False, "flowReservation": False}
    return {
        "schemaVersion": "1.0",
        "adapter": ADAPTER,
        "adapterVersion": ADAPTER_VERSION,
        "contractVersion": CONTRACT_VERSION,
        "upstream": {
            "name": "EPRI IEEE 2030.5 Client",
            "repository": "https://github.com/epri-dev/IEEE-2030.5-Client",
            "version": UPSTREAM_VERSION,
            "revision": UPSTREAM_REVISION,
            "license": "BSD-3-Clause",
            "language": "C",
            "independentEngine": True,
        },
        "tls": {"versions": ["1.2"], "cipherSuites": ["TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8"]},
        "versions": {
            edition: {"client": client, "server": server, "features": features, "notes": notes}
            for edition in CLIENT_EDITIONS
        },
    }


def print_fixture(args: list[str]) -> int:
    if len(args) > 1:
        raise UsageError("print-fixture takes at most one name")
    name = args[0] if args else "baseline"
    if "/" in name or name.startswith("."):
        raise UsageError(f"{name!r} is not a fixture name")
    try:
        data = (FIXTURES / name / "fixture.json").read_bytes()
    except OSError:
        raise CodedError(EXIT_FAILED, f"no fixture named {name!r}") from None
    sys.stdout.buffer.write(data)
    sys.stdout.flush()
    return EXIT_OK


def load_fixture(path: str) -> dict[str, Any]:
    """Reads a fixture; a broken one is exit 4. The schema is checked by make fixtures."""
    try:
        fixture = json.loads(Path(path).read_text())
        int(fixture["device"]["pin"])
        for program in fixture["derPrograms"]:
            program["description"]
            for control in program["controls"]:
                control["base"], control["duration"]
    except (OSError, ValueError, KeyError, TypeError) as exc:
        raise CodedError(EXIT_FIXTURE, f"fixture {path}: {exc!r}") from None
    return fixture


# ---- client ----


def run_call(args: list[str]) -> int:
    """call <operation>: the epri adapter has no control socket (no client run)."""
    if not args or args[0].startswith("--"):
        raise UsageError("call needs an operation")
    op, args = args[0], args[1:]
    flags = parse_flags(args, "device", "payload", "timeout-ms", "control-socket")
    millis(flags, "timeout-ms", 30.0)
    if "device" in flags and not flags["device"].isdigit():
        raise UsageError(f"--device {flags['device']!r} is not an SFDI")
    if "payload" in flags:
        try:
            json.loads(flags["payload"])
        except ValueError:
            raise UsageError("--payload is not JSON") from None
    emit({
        "schemaVersion": "1.0",
        "adapter": ADAPTER,
        "operation": op,
        "ok": False,
        "error": {"code": "unsupported", "detail": "the epri adapter has no client run and offers no call operation"},
        "request": None,
        "response": None,
        "elapsedMs": 0,
    })
    return EXIT_UNSUPPORTED


def host_port(value: str, flag: str, need_port: bool) -> tuple[str, int | None]:
    """host:port (or host[:port] when need_port is false), with an IPv6 host in brackets."""
    match = re.fullmatch(r"(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9._-]+)(?::(\d{1,5}))?", value)
    port = int(match.group(2)) if match and match.group(2) else None
    if not match or (need_port and port is None) or (port is not None and not 0 < port < 65536):
        raise UsageError(f"--{flag} {value!r} is not {'host:port' if need_port else 'HOST[:PORT]'}")
    return match.group(1), port


def unsupported_detail(op: str, edition: str, fixture: dict[str, Any]) -> str | None:
    """Why the adapter does not perform op, or None when it does."""
    if op not in PERFORMED:
        reasons = {
            "subscribe": "subscriptions are commented out in client_test",
            "log-event": "client_test posts a LogEvent of its own (\"Alarms Test\"), not the fixture's",
            "put-der": "client_test PUTs the DER settings of its own ./settings files, not the fixture's",
        }
        return reasons.get(op, f"client_test has no {op} operation")
    if edition not in CLIENT_EDITIONS:
        return f"the EPRI client is run against IEEE 2030.5 {', '.join(CLIENT_EDITIONS)} only"
    if op in ("poll-controls", "respond"):
        for program in fixture["derPrograms"]:
            if program.get("defaultControl") or program.get("curves"):
                return (f"the DER program {program['description']!r} has a default control or curves, "
                        "which the adapter does not check")
    return None


def run_client(args: list[str]) -> int:
    if not args:
        raise UsageError("client needs an operation")
    op, args = args[0], args[1:]
    flags = parse_flags(args, "version", "url", "tls-dir", "fixture", "connect-timeout-ms", "timeout-ms", "payload",
                        "notify-listen", "notify-host")
    edition = edition_of(flags)
    url = urlsplit(required(flags, "url"))
    if url.scheme != "https" or not url.hostname:
        raise UsageError(f"--url {flags['url']!r} is not an https:// URL")
    connect_timeout = millis(flags, "connect-timeout-ms", 10.0)
    timeout = millis(flags, "timeout-ms", 30.0)
    host_port(flags.get("notify-listen", "0.0.0.0:8443"), "notify-listen", True)
    if "notify-host" in flags:
        host_port(flags["notify-host"], "notify-host", False)
    if "payload" in flags:
        try:
            json.loads(flags["payload"])
        except ValueError:
            raise UsageError("--payload is not JSON") from None
    if op not in CONTRACT_OPERATIONS + ("run",):
        raise UsageError(f"unknown client operation {op!r}")
    tls_dir = Path(required(flags, "tls-dir"))
    if op == "run":
        raise CodedError(EXIT_UNSUPPORTED, "client run is not implemented by the epri adapter")
    fixture = load_fixture(flags.get("fixture", str(DEFAULT_FIXTURE)))

    result: dict[str, Any] = {"request": None, "response": None}
    start = time.monotonic()
    error, code = None, EXIT_OK
    reason = unsupported_detail(op, edition, fixture)
    if reason:
        error, code = {"code": "unsupported", "detail": reason}, EXIT_UNSUPPORTED
    else:
        for name in ("client.crt", "client.key", "ca.crt"):
            if not (tls_dir / name).is_file():
                raise CodedError(EXIT_FIXTURE, f"--tls-dir {tls_dir} has no {name}")
        try:
            perform(op, url, tls_dir, fixture, connect_timeout, timeout, result)
        except OperationError as exc:
            error = {"code": exc.code, "detail": str(exc)}
            code = EXIT_CONNECT if exc.code == "connect-failed" else EXIT_FAILED
    emit({
        "schemaVersion": "1.0",
        "adapter": ADAPTER,
        "version": edition,
        "operation": op,
        "ok": code == EXIT_OK,
        "error": error,
        "request": result["request"],
        "response": result["response"],
        "elapsedMs": int((time.monotonic() - start) * 1000),
    })
    return code


def interface_name() -> str:
    """The interface of the default route, which client_test binds to."""
    try:
        for line in Path("/proc/net/route").read_text().splitlines()[1:]:
            fields = line.split()
            if len(fields) > 1 and fields[1] == "00000000":
                return fields[0]
    except OSError:
        pass
    names = sorted(n for n in os.listdir("/sys/class/net") if n != "lo")
    return names[0] if names else "lo"


def device_files(tls_dir: Path) -> Path:
    """client_test reads a .x509 device certificate as DER, and its key from
    the .pem file of the same name."""
    WORK.mkdir(parents=True, exist_ok=True)
    pem = (tls_dir / "client.crt").read_text()
    match = re.search(r"-----BEGIN CERTIFICATE-----(.+?)-----END CERTIFICATE-----", pem, re.S)
    if not match:
        raise OperationError("invalid-request", f"{tls_dir / 'client.crt'} holds no PEM certificate")
    cert = WORK / "device.x509"
    cert.write_bytes(base64.b64decode("".join(match.group(1).split())))
    key = WORK / "device.pem"
    key.write_bytes((tls_dir / "client.key").read_bytes())
    key.chmod(0o600)
    return cert


class Transcript:
    """What client_test printed: the documents it parsed, by type and path,
    and the markers of its tests."""

    response_line = re.compile(r"^(GET|POST|PUT|DELETE) (\S+): (\d+)$")

    def __init__(self) -> None:
        self.lines: list[str] = []
        self.documents: list[tuple[str, str, ET.Element]] = []  # (path, tag, element)
        self.requests: list[tuple[str, str]] = []
        self.responded = False
        # Every exchange: method, path, status, the document sent (tag and
        # element, for a POST or PUT) and the Location header.
        self.exchanges: list[dict[str, Any]] = []
        self._path: str | None = None
        self._doc: list[str] = []
        self._sending = False
        self._sent: list[str] = []
        self._sent_doc: ET.Element | None = None
        self._location: str | None = None

    def add(self, line: str) -> None:
        self.lines.append(line)
        if line == "se_send:":
            self._sending, self._sent, self._sent_doc = True, [], None
            return
        if self._sending:
            if self._sent or (line.startswith("<") and not line.startswith("<--") and not line.startswith("</")):
                self._sent.append(line)
                if line.startswith("</") or (len(self._sent) == 1 and line.rstrip().endswith("/>")):
                    try:
                        self._sent_doc = ET.fromstring("\n".join(self._sent))
                    except ET.ParseError:
                        self._sent_doc = None
                    self._sending, self._sent = False, []
            return
        if line.startswith("<-- conn"):
            self._location = None
        elif line.lower().startswith("location:"):
            self._location = line.split(":", 1)[1].strip()
        if self._doc:
            self._doc.append(line)
            if line.startswith("</") or (len(self._doc) == 1 and line.rstrip().endswith("/>")):
                self._close()
            return
        match = self.response_line.match(line)
        if match:
            self.responded = True
            self.requests.append((match.group(1), match.group(2)))
            self._path = match.group(2)
            sent = self._sent_doc if match.group(1) in ("POST", "PUT") else None
            self.exchanges.append({"method": match.group(1), "path": match.group(2), "status": int(match.group(3)),
                                   "tag": sent.tag.split("}")[-1] if sent is not None else None,
                                   "sent": sent, "location": self._location})
            self._sent_doc, self._location = None, None
        elif self._path and line.startswith("<") and not line.startswith("</"):
            self._doc = [line]
            if line.rstrip().endswith("/>"):
                self._close()

    def _close(self) -> None:
        text = "\n".join(self._doc)
        self._doc = []
        try:
            element = ET.fromstring(text)
        except ET.ParseError:
            return
        tag = element.tag.split("}")[-1]
        self.documents.append((self._path or "", tag, element))
        self._path = None

    def of(self, tag: str) -> list[tuple[str, ET.Element]]:
        return [(path, element) for path, t, element in self.documents if t == tag]

    def has(self, text: str) -> bool:
        return any(text in line for line in self.lines)


def child(element: ET.Element, name: str) -> ET.Element | None:
    for c in element:
        if c.tag.split("}")[-1] == name:
            return c
    return None


def children(element: ET.Element, name: str) -> list[ET.Element]:
    return [c for c in element if c.tag.split("}")[-1] == name]


def text_of(element: ET.Element | None, name: str) -> str | None:
    c = child(element, name) if element is not None else None
    return c.text.strip() if c is not None and c.text else None


def perform(op: str, url: Any, tls_dir: Path, fixture: dict[str, Any],
            connect_timeout: float, timeout: float, result: dict[str, Any]) -> None:
    try:
        address = socket.getaddrinfo(url.hostname, url.port or 443, socket.AF_INET, socket.SOCK_STREAM)[0][4][0]
    except (socket.gaierror, IndexError) as exc:
        raise OperationError("connect-failed", f"{url.hostname}: {exc}") from None
    try:
        with socket.create_connection((address, url.port or 443), timeout=connect_timeout):
            pass
    except OSError as exc:
        raise OperationError("connect-failed", f"{address}:{url.port or 443}: {exc}") from None
    if op == "register" and fixture["device"]["pin"] != 111115:
        raise OperationError("invalid-request", "client_test registers with the PIN 111115 only, "
                                                f"the fixture's is {fixture['device']['pin']}")
    cert = device_files(tls_dir)
    target = f"https://{address}:{url.port or 443}{url.path or '/dcap'}"
    argv = ["stdbuf", "-oL", str(EPRI / "client_test"), interface_name(), str(cert), str(tls_dir / "ca.crt"),
            target, *COMMANDS[op]]
    env = {**os.environ, "OPENSSL_CONF": str(EPRI / "openssl.cnf")}
    proc = subprocess.Popen(argv, cwd=EPRI, env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                            stderr=subprocess.STDOUT, text=True, errors="replace")
    lines: queue.Queue[str | None] = queue.Queue()

    def read() -> None:
        assert proc.stdout is not None
        for line in proc.stdout:
            lines.put(line.rstrip("\n"))
        lines.put(None)

    threading.Thread(target=read, daemon=True).start()
    transcript = Transcript()
    deadline = time.monotonic() + timeout
    connect_deadline = time.monotonic() + connect_timeout
    outcome: tuple[str, str] | None = None  # (error code, detail); ("", "") is success
    try:
        while outcome is None:
            now = time.monotonic()
            if now > deadline:
                outcome = ("timeout", f"no result within {timeout:g} s")
                break
            if not transcript.responded and now > connect_deadline:
                outcome = ("connect-failed", f"no response from {target} within {connect_timeout:g} s")
                break
            try:
                line = lines.get(timeout=0.2)
            except queue.Empty:
                continue
            if line is None:  # client_test ended: it ends only on a failure
                outcome = ended(transcript, proc)
                break
            print(line, file=sys.stderr, flush=True)
            transcript.add(line)
            outcome = progress(op, transcript, fixture)
    finally:
        if proc.poll() is None:
            proc.kill()
        proc.wait()
    if transcript.requests:
        method, path = transcript.requests[-1]
        result["request"] = {"method": method, "path": path}
    result["response"] = summary(op, transcript)
    if outcome != ("", ""):
        raise OperationError(*outcome)


def ended(transcript: Transcript, proc: subprocess.Popen[str]) -> tuple[str, str]:
    """Why client_test ended before the operation was complete."""
    code = proc.wait()
    tls = [line for line in transcript.lines if line.startswith("tls_") or "verify" in line.lower()]
    if code < 0:
        return "server-error", f"client_test was killed by signal {-code} (after {last_request(transcript)})"
    if not transcript.responded:
        return "connect-failed", (tls[-1] if tls else transcript.lines[-1] if transcript.lines else "no output")
    failed = [line for line in transcript.lines if " failed: " in line or "parse error" in line]
    return "server-error", (failed[-1] if failed else transcript.lines[-1])


def last_request(transcript: Transcript) -> str:
    requests = [line for line in transcript.lines if re.match(r"^(GET|POST|PUT|DELETE) /\S* HTTP/1\.1$", line)]
    return requests[-1] if requests else "no request"


def progress(op: str, transcript: Transcript, fixture: dict[str, Any]) -> tuple[str, str] | None:
    """The outcome once the transcript holds one; None while it does not."""
    last = transcript.lines[-1]
    if " failed: " in last:
        return "server-error", last
    if op == "discover":
        return ("", "") if transcript.of("DeviceCapability") else None
    if op == "register":
        return ("", "") if last == "registration succeeded" else None
    if op == "report":
        return report_outcome(transcript)
    if op == "respond":
        return respond_outcome(transcript, fixture)
    # poll-controls: complete when every DERControlList of every program
    # the server lists has been read.
    programs = [p for _, lst in transcript.of("DERProgramList") for p in children(lst, "DERProgram")]
    if not programs:
        return None
    lists = {path: element for path, element in transcript.of("DERControlList")}
    wanted = [child(p, "DERControlListLink").get("href") for p in programs if child(p, "DERControlListLink") is not None]
    if not all(href in lists for href in wanted):
        return None
    served: dict[str, list[ET.Element]] = {}
    for p in programs:
        link = child(p, "DERControlListLink")
        controls = children(lists[link.get("href")], "DERControl") if link is not None else []
        served.setdefault(text_of(p, "description") or "", []).extend(controls)
    for program in fixture["derPrograms"]:
        if program["description"] not in served:
            return "server-error", f"the server does not serve the fixture's DER program {program['description']!r}"
        for control in program["controls"]:
            if not any(control_matches(control, c) for c in served[program["description"]]):
                return "server-error", (f"the server does not serve the fixture's control "
                                        f"{control['description']!r} of {program['description']!r}")
    return "", ""


def mup_list(transcript: Transcript) -> str | None:
    """The MirrorUsagePointList href of the DeviceCapability."""
    for _, dcap in transcript.of("DeviceCapability"):
        link = child(dcap, "MirrorUsagePointListLink")
        if link is not None and link.get("href"):
            return link.get("href")
    return None


def report_outcome(transcript: Transcript) -> tuple[str, str] | None:
    """ok once the server created client_test's MirrorUsagePoint: 201 with a Location."""
    href = mup_list(transcript)
    for x in transcript.exchanges:
        if x["method"] != "POST" or x["tag"] != "MirrorUsagePoint" or (href and x["path"] != href):
            continue
        if x["status"] == 201 and x["location"]:
            return "", ""
        if x["status"] == 201:
            return "server-error", f"POST {x['path']}: 201 without a Location"
        return "server-error", f"POST {x['path']} of the MirrorUsagePoint: {x['status']}"
    return None


def active_control(transcript: Transcript, fixture: dict[str, Any]) -> ET.Element | None:
    """The served DERControl that matches the fixture's control active now (start 0)."""
    wanted = [(p["description"], c) for p in fixture["derPrograms"] for c in p["controls"] if c.get("start", 0) == 0]
    lists = {path: element for path, element in transcript.of("DERControlList")}
    for _, lst in transcript.of("DERProgramList"):
        for p in children(lst, "DERProgram"):
            link = child(p, "DERControlListLink")
            if link is None or link.get("href") not in lists:
                continue
            for description, control in wanted:
                if text_of(p, "description") != description:
                    continue
                for served in children(lists[link.get("href")], "DERControl"):
                    if control_matches(control, served):
                        return served
    return None


def same_path(reply_to: str, path: str) -> bool:
    """replyTo may be a path or an absolute URL."""
    return reply_to == path or urlsplit(reply_to).path == path


def respond_outcome(transcript: Transcript, fixture: dict[str, Any]) -> tuple[str, str] | None:
    """ok once a DERControlResponse to the replyTo of the active control was accepted (201 or 204)."""
    control = active_control(transcript, fixture)
    if control is None:
        return None
    reply_to = control.get("replyTo")
    if not reply_to:
        return "server-error", "the control active now has no replyTo"
    subject = (text_of(control, "mRID") or "").upper()
    for x in transcript.exchanges:
        if x["method"] != "POST" or x["tag"] != "DERControlResponse" or not same_path(reply_to, x["path"]):
            continue
        if (text_of(x["sent"], "subject") or "").upper() != subject:
            continue
        if x["status"] in (201, 204):
            return "", ""
        return "server-error", f"POST {x['path']} of a DERControlResponse: {x['status']}"
    return None


def control_matches(control: dict[str, Any], served: ET.Element) -> bool:
    """Whether a served DERControl has the fixture control's duration and DERControlBase values.

    client_test prints an optional boolean it parsed as true whenever it is
    present: its output code reads the presence bit, not the value bit after
    it (output.c, compared with parse.c). Its printout shows that a boolean
    element was served, not its value, so booleans match by presence."""
    interval, base = child(served, "interval"), child(served, "DERControlBase")
    if interval is None or base is None or text_of(interval, "duration") != str(control["duration"]):
        return False
    got: dict[str, Any] = {}
    want: dict[str, Any] = {}
    for name, value in control["base"].items():
        want[name] = True if isinstance(value, bool) else value
    for name in ("opModConnect", "opModEnergize"):
        if text_of(base, name) is not None:
            got[name] = True
    for name in ("opModMaxLimW", "opModFixedW"):
        value = text_of(base, name)
        if value is not None:
            got[name] = int(value)
    return got == want


def summary(op: str, transcript: Transcript) -> dict[str, Any] | None:
    """The informative response body: what client_test read."""
    if op == "discover":
        docs = transcript.of("DeviceCapability")
        if not docs:
            return None
        path, dcap = docs[-1]
        links = sorted(c.tag.split("}")[-1] for c in dcap if c.get("href"))
        return {"status": 200, "body": {"href": dcap.get("href"), "pollRate": dcap.get("pollRate"), "links": links}}
    if op == "report":
        posted = [x for x in transcript.exchanges if x["tag"] == "MirrorUsagePoint"]
        if not posted:
            return None
        x, sent = posted[-1], posted[-1]["sent"]
        return {"status": x["status"], "body": {
            "location": x["location"],
            "mirrorUsagePoint": {"mRID": text_of(sent, "mRID"), "roleFlags": text_of(sent, "roleFlags")},
            "note": "client_test posts a MirrorUsagePoint of its own (random mRID, roleFlags 09; its meter test "
                    "then posts seven readings with hard-coded values), not the fixture's"}}
    if op == "respond":
        posted = [x for x in transcript.exchanges if x["tag"] == "DERControlResponse"]
        if not posted:
            return None
        return {"status": posted[-1]["status"], "body": {"responses": [
            {"path": x["path"], "status": x["status"], "subject": text_of(x["sent"], "subject"),
             "responseStatus": text_of(x["sent"], "status")} for x in posted]}}
    if op == "register":
        registrations = transcript.of("Registration")
        if not registrations:
            return None
        return {"status": 200, "body": {"registration": registrations[-1][0],
                                        "registrationPIN": text_of(registrations[-1][1], "pIN")}}
    programs = []
    lists = {path: element for path, element in transcript.of("DERControlList")}
    for _, lst in transcript.of("DERProgramList"):
        for p in children(lst, "DERProgram"):
            link = child(p, "DERControlListLink")
            controls = children(lists[link.get("href")], "DERControl") if link is not None and link.get("href") in lists else []
            programs.append({"description": text_of(p, "description"), "primacy": text_of(p, "primacy"),
                             "controls": [text_of(c, "description") for c in controls]})
    return {"status": 200, "body": {"derPrograms": programs}} if programs else None


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

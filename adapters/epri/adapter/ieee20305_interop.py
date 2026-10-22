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
CONTRACT_VERSION = "0.2"
EDITIONS = ("2018", "2023")  # what --version accepts
CLIENT_EDITIONS = ("2018",)  # what EPRI's client is run against
OPERATIONS = ("register", "discover", "poll-controls")
FIXTURES = Path("/fixtures")
DEFAULT_FIXTURE = FIXTURES / "baseline" / "fixture.json"
EPRI = Path("/opt/epri")
WORK = Path("/tmp/ieee20305-interop")

ADAPTER_VERSION = os.environ.get("IEEE20305_INTEROP_ADAPTER_VERSION", "dev")
UPSTREAM_VERSION = os.environ.get("IEEE20305_INTEROP_UPSTREAM_VERSION", "unknown")
UPSTREAM_REVISION = os.environ.get("IEEE20305_INTEROP_UPSTREAM_REVISION", "unknown")

# client_test's own command for each operation; discover is a plain
# retrieval of the URL.
COMMANDS = {"discover": [], "register": ["inverter", "register"], "poll-controls": ["inverter", "all"]}


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
            raise CodedError(EXIT_UNSUPPORTED, "call: the control socket is not implemented by the epri adapter")
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
    notes = [
        "EPRI's client_test (v0.2.11, unmaintained since 2018), built with its own build script against "
        "Debian trixie's OpenSSL 3, with one patch: the SHA-256 padding of its LFDI computation, which crashed "
        "for one device certificate in eight (adapters/epri/patches).",
        "TLS: TLS 1.2 with TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8 only, the client's own cipher list; OpenSSL runs "
        "at security level 0 (adapters/epri/openssl.cnf), under which OpenSSL 3 offers CCM_8 as OpenSSL 1.1.0 did.",
        "Schema: EPRI's sep.xsd 2.0.4. Its parser accepts no element outside that schema, such as a CSIP-AUS "
        "extension element.",
        "client_test prints a present optional boolean as true whatever its value (its output code reads the "
        "presence bit), so poll-controls matches the fixture's boolean control elements by presence only.",
        "Connects by IP address (the adapter resolves the URL's host name) and does not check the server's host "
        "name against its certificate.",
        "register checks for the PIN 111115, which client_test hard-codes; the adapter checks the fixture's PIN "
        "is that.",
        "report is not offered: client_test's metering test posts a MirrorUsagePoint of its own, not the "
        "fixture's. Subscriptions are commented out in client_test.",
    ]
    client = {"supported": True, "operations": list(OPERATIONS)}
    server = {"supported": False, "operations": []}
    features = {"mTLS": True, "csip": True, "csipAus": False, "subscription": False}
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


def run_client(args: list[str]) -> int:
    if not args:
        raise UsageError("client needs an operation")
    op, args = args[0], args[1:]
    flags = parse_flags(args, "version", "url", "tls-dir", "fixture", "connect-timeout-ms", "timeout-ms", "payload")
    edition = edition_of(flags)
    url = urlsplit(required(flags, "url"))
    if url.scheme != "https" or not url.hostname:
        raise UsageError(f"--url {flags['url']!r} is not an https:// URL")
    connect_timeout = millis(flags, "connect-timeout-ms", 10.0)
    timeout = millis(flags, "timeout-ms", 30.0)
    if op not in OPERATIONS + ("report", "subscribe", "run"):
        raise UsageError(f"unknown client operation {op!r}")
    tls_dir = Path(required(flags, "tls-dir"))
    fixture = load_fixture(flags.get("fixture", str(DEFAULT_FIXTURE)))
    if op == "run":
        raise CodedError(EXIT_UNSUPPORTED, "client run is not implemented by the epri adapter")
    for name in ("client.crt", "client.key", "ca.crt"):
        if not (tls_dir / name).is_file():
            raise CodedError(EXIT_FIXTURE, f"--tls-dir {tls_dir} has no {name}")

    result: dict[str, Any] = {"request": None, "response": None}
    start = time.monotonic()
    error, code = None, EXIT_OK
    if op in ("report", "subscribe"):
        reason = ("client_test posts a MirrorUsagePoint of its own, not the fixture's" if op == "report"
                  else "subscriptions are commented out in client_test")
        error, code = {"code": "unsupported", "detail": reason}, EXIT_UNSUPPORTED
    elif edition not in CLIENT_EDITIONS:
        error, code = {"code": "unsupported", "detail": f"the EPRI client is run against IEEE 2030.5 "
                                                       f"{', '.join(CLIENT_EDITIONS)} only"}, EXIT_UNSUPPORTED
    else:
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
        self._path: str | None = None
        self._doc: list[str] = []

    def add(self, line: str) -> None:
        self.lines.append(line)
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

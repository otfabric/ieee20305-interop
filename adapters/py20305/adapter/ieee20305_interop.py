# SPDX-License-Identifier: MIT
"""ieee20305-interop adapter for py20305, the DER Security IEEE 2030.5 client.

Drives the py20305 protocol engine (CsipClient, its discovery, its HTTP
client, its notification listener and its models) behind the commands of
docs/CONTAINER_CONTRACT.md. py20305 is a client only: the server command
validates its flags and exits 5, and call answers unsupported.
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import socket
import ssl
import sys
import time
from dataclasses import dataclass, field
from fractions import Fraction
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

EXIT_OK, EXIT_FAILED, EXIT_USAGE, EXIT_CONNECT, EXIT_FIXTURE, EXIT_UNSUPPORTED = 0, 1, 2, 3, 4, 5

ADAPTER = "py20305"
CONTRACT_VERSION = "0.3"
EDITIONS = ("2018", "2023")
# Every client operation of the contract; the adapter performs all but these.
CLIENT_OPERATIONS = ("register", "discover", "poll-controls", "report", "subscribe", "respond", "log-event",
                     "put-der", "flow-reservation", "poll-pricing")
NOT_IMPLEMENTED = {
    "flow-reservation": "py20305 has no flow reservation client logic (no FlowReservationRequest is ever built or "
                        "POSTed by its engine), so the adapter does not perform flow-reservation",
}
# What the self-tests exercise and pass, per IEEE 2030.5 version (R4.2): the
# GridAPPS-D server serves 2018 and 2023 (baseline only), Envoy 2018
# (baseline and csip-aus). put-der and poll-pricing are performed against
# Envoy, the only server that serves them, where upstream defects make them
# fail (tests/known-bugs.tsv); curves (csip) are checked, but no server of
# the self-tests serves the csip fixture, so it is not declared.
DECLARED_OPERATIONS = ["register", "discover", "poll-controls", "report", "subscribe", "respond", "log-event"]
DECLARED = {
    "2018": {
        "operations": DECLARED_OPERATIONS + ["put-der", "poll-pricing"],
        "fixtures": ["baseline", "csip", "csip-aus"],
        "features": {"csipAus": True, "subscription": True, "defaultControl": True, "curves": False,
                     "pricing": False},
    },
    "2023": {
        "operations": DECLARED_OPERATIONS,
        "fixtures": ["baseline", "csip"],
        "features": {"csipAus": False, "subscription": True, "defaultControl": True, "curves": False,
                     "pricing": False},
    },
}
CALLS = ("issue-control",)
FIXTURES = Path("/fixtures")
DEFAULT_FIXTURE = FIXTURES / "baseline" / "fixture.json"
DEFAULT_NOTIFY_LISTEN = "0.0.0.0:8443"

# Set by the Dockerfile.
ADAPTER_VERSION = os.environ.get("IEEE20305_INTEROP_ADAPTER_VERSION", "dev")
UPSTREAM_VERSION = os.environ.get("IEEE20305_INTEROP_UPSTREAM_VERSION", "unknown")
UPSTREAM_REVISION = os.environ.get("IEEE20305_INTEROP_UPSTREAM_REVISION", "unknown")

# What py20305 offers (py20305.client.tls): TLS 1.2 with these suites only.
TLS_VERSIONS = ["1.2"]
CIPHER_SUITES = ["TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384", "TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8"]

# DERControlBase elements of the fixture schema, by their py20305 attribute.
PLAIN_MODES = {"opModConnect": "op_mod_connect", "opModEnergize": "op_mod_energize",
               "opModMaxLimW": "op_mod_max_lim_w", "opModFixedW": "op_mod_fixed_w"}
CURVE_MODES = {"opModVoltVar": "op_mod_volt_var", "opModVoltWatt": "op_mod_volt_watt",
               "opModFreqWatt": "op_mod_freq_watt", "opModWattPF": "op_mod_watt_pf"}
LIMIT_MODES = ("opModExpLimW", "opModImpLimW", "opModGenLimW", "opModLoadLimW")


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
    # py20305's own log, on stderr. IEEE20305_INTEROP_LOG_LEVEL=DEBUG shows every request.
    level = os.environ.get("IEEE20305_INTEROP_LOG_LEVEL", "WARNING").upper()
    logging.basicConfig(stream=sys.stderr, level=getattr(logging, level, logging.WARNING),
                        format="%(levelname)s %(name)s: %(message)s")
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
            raise CodedError(EXIT_UNSUPPORTED, "py20305 is a client only: there is no server role")
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
            print(f"ieee20305-interop {ADAPTER} {ADAPTER_VERSION} (py20305 {UPSTREAM_VERSION})")
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


def host_port(value: str, flag: str, need_port: bool) -> tuple[str, int | None]:
    """HOST:PORT (or HOST alone when the port is optional); [v6]:port is accepted."""
    host, port = value, None
    if value.startswith("["):
        end = value.find("]")
        if end < 0:
            raise UsageError(f"--{flag} {value!r} is not host:port")
        host, rest = value[1:end], value[end + 1:]
        if rest:
            if not rest.startswith(":"):
                raise UsageError(f"--{flag} {value!r} is not host:port")
            port = rest[1:]
    elif ":" in value:
        host, port = value.rsplit(":", 1)
    if not host or any(c in host for c in "/ ?#@"):
        raise UsageError(f"--{flag} {value!r} has no valid host")
    if port is None:
        if need_port:
            raise UsageError(f"--{flag} {value!r} is not host:port")
        return host, None
    if not port.isdigit() or not 0 < int(port) < 65536:
        raise UsageError(f"--{flag} {value!r} has no valid port")
    return host, int(port)


# ---- capabilities and fixtures ----


def capabilities() -> dict[str, Any]:
    common_notes = [
        "TLS: TLS 1.2 with TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384 and TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8, "
        "through Python's ssl module (OpenSSL), as py20305 configures it (client/tls.py). The notification "
        "listener offers the same suites (subscription/notification_server.py:_create_server_ssl_context).",
        "Client only: py20305 has no server role, and no control socket, so call is unsupported.",
        "Verifies the server's host name against its certificate (TlsConfig.check_hostname, on by default), "
        "so a server certificate must name the host the client connects to.",
        "Operations run on the py20305 protocol engine: CsipClient discovery (DER programs, default controls, "
        "controls, curves, tariffs), Sep2Client for every request, py20305's IEEE 2030.5 models for what it "
        "sends, csipaus.elements.doe_limits for the CSIP-AUS limits, and NotificationServer for subscribe.",
        "subscribe: py20305's NotificationServer serves POST /notify with notify.crt and notify.key of --tls-dir "
        "(the device certificate when they are absent), and accepts a server that presents no client "
        "certificate (client_cert_mode warn).",
        "respond, log-event and the subscription are POSTed with Sep2Client.post directly, not through "
        "post_der_response or SubscriptionManager.subscribe, which log a refusal instead of raising it "
        "(events/response.py post_der_response, subscription/manager.py subscribe).",
        "put-der sends the fixture's der, with its CSIP-AUS doeModesSupported and doeModesEnabled when it has "
        "them, as py20305's csipaus models (models/csipaus/csipaus_ext.py) in py20305's CSIP-AUS mode.",
        "flow-reservation is unsupported (exit 5): py20305 v0.16.0 has no flow reservation client logic.",
        "Messages (MessagingProgram) are not read: no contract operation reads them.",
        "put-der (2018) fails against Envoy, a known issue: py20305 writes the CSIP-AUS extensions in the namespace "
        "https://csipaus.org/ns (models/csipaus/csipaus_ext.py:12, xml/serialization.py:39), Envoy reads only "
        "https://csipaus.org/ns/v1.3 (envoy_schema 2.7.0 server/schema/sep2/base.py:8) and answers the "
        "DERCapability PUT with 400, doeModesSupported missing (with the v1.3 namespace Envoy accepts all four "
        "PUTs); GridAPPS-D serves no DERList.",
        "poll-pricing (2018) fails against Envoy, a known issue: Envoy puts a RateComponentLink in the IEEE 2030.5 "
        "namespace into each TimeTariffInterval (src/envoy/server/mapper/sep2/pricing.py:373), which neither "
        "sep.xsd nor the CSIP-AUS schema define, and py20305's parser, which has no lenient option "
        "(xml/serialization.py from_xml), rejects the TimeTariffIntervalList "
        "(discovery.py _discover_rate_component skips it), so no interval is found; GridAPPS-D serves no "
        "tariffs.",
        "Curves are checked, but not declared: no server of the self-tests serves curves (GridAPPS-D cannot "
        "seed them, Envoy serves none), so no baked fixture has them.",
    ]
    by_edition = {
        "2018": ["--version 2018 sets py20305's server_2018_compat: DERStatus carries genConnectStatus, a "
                 "DERControlResponse no modesResponded, and root elements no schema attributes."],
        "2023": ["Against GridAPPS-D, the only 2023 server of the self-tests: the baseline and csip fixtures; "
                 "no CSIP-AUS is exercised."],
    }
    versions = {}
    for edition in EDITIONS:
        declared = DECLARED[edition]
        versions[edition] = {
            "client": {"supported": True, "operations": declared["operations"], "fixtures": declared["fixtures"]},
            "server": {"supported": False, "operations": []},
            "features": {"mTLS": True, "csip": True, "messaging": False, "flowReservation": False,
                         **declared["features"]},
            "notes": common_notes + by_edition[edition],
        }
    return {
        "schemaVersion": "1.0",
        "adapter": ADAPTER,
        "adapterVersion": ADAPTER_VERSION,
        "contractVersion": CONTRACT_VERSION,
        "upstream": {
            "name": "DER Security py20305",
            "repository": "https://github.com/DERSecurity/py20305",
            "version": UPSTREAM_VERSION,
            "revision": UPSTREAM_REVISION,
            "license": "Apache-2.0",
            "language": "Python",
            "independentEngine": True,
        },
        "tls": {"versions": TLS_VERSIONS, "cipherSuites": CIPHER_SUITES},
        "versions": versions,
    }


def print_fixture(args: list[str]) -> int:
    if len(args) > 1:
        raise UsageError("print-fixture takes at most one name")
    name = args[0] if args else "baseline"
    if "/" in name or name.startswith("."):
        raise UsageError(f"{name!r} is not a fixture name")
    path = FIXTURES / name / "fixture.json"
    try:
        data = path.read_bytes()
    except OSError:
        raise CodedError(EXIT_FAILED, f"no fixture named {name!r}") from None
    sys.stdout.buffer.write(data)
    sys.stdout.flush()
    return EXIT_OK


@dataclass
class Fixture:
    name: str
    serial: str
    pin: int
    device_category: int
    der_programs: list[dict[str, Any]] = field(default_factory=list)
    report: dict[str, Any] = field(default_factory=dict)
    der: dict[str, Any] | None = None
    log_event: dict[str, Any] | None = None
    tariffs: list[dict[str, Any]] = field(default_factory=list)


def load_fixture(path: str) -> Fixture:
    """Reads a fixture; a broken one is exit 4. The schema is checked by make fixtures."""
    try:
        doc = json.loads(Path(path).read_text())
        device = doc["device"]
        fixture = Fixture(
            name=doc["name"],
            serial=device["serial"],
            pin=int(device["pin"]),
            device_category=int(device["deviceCategory"], 16),
            der_programs=list(doc["derPrograms"]),
            report=dict(doc["report"]),
            der=doc.get("der"),
            log_event=doc.get("logEvent"),
            tariffs=list(doc.get("tariffs", [])),
        )
        for program in fixture.der_programs:
            curves = {c["mRID"]: c for c in program.get("curves", [])}
            for curve in curves.values():
                curve["curveType"], [(p["x"], p["y"]) for p in curve["points"]]
            bases = [c["base"] for c in program["controls"]] + (
                [program["defaultControl"]["base"]] if "defaultControl" in program else [])
            program["description"], [c["duration"] for c in program["controls"]]
            for base in bases:
                for name in base:
                    if name in CURVE_MODES and base[name] not in curves:
                        raise ValueError(f"{name} names {base[name]}, not a curve of {program['description']!r}")
                    if name in LIMIT_MODES:
                        base[name]["value"], base[name]["multiplier"]
        fixture.report["mirrorUsagePoint"]["mRID"], fixture.report["meterReadings"][0]["readingType"]
        for tariff in fixture.tariffs:
            tariff["description"], tariff["currency"], tariff["pricePowerOfTenMultiplier"]
            for rc in tariff["rateComponents"]:
                rc["description"], [(i["duration"], i["price"]) for i in rc["timeTariffIntervals"]]
    except (OSError, ValueError, KeyError, TypeError, IndexError) as exc:
        raise CodedError(EXIT_FIXTURE, f"fixture {path}: {exc!r}") from None
    return fixture


# ---- call ----


def run_call(args: list[str]) -> int:
    """call <operation>: py20305 has no control socket, so every operation is unsupported."""
    if not args or args[0].startswith("--"):
        raise UsageError("call needs an operation: " + ", ".join(CALLS))
    op, args = args[0], args[1:]
    flags = parse_flags(args, "device", "payload", "timeout-ms", "control-socket")
    millis(flags, "timeout-ms", 30.0)
    if "payload" in flags:
        try:
            json.loads(flags["payload"])
        except ValueError:
            raise UsageError(f"--payload is not JSON: {flags['payload']!r}") from None
    emit({
        "schemaVersion": "1.0",
        "adapter": ADAPTER,
        "operation": op,
        "ok": False,
        "error": {"code": "unsupported",
                  "detail": "py20305 is a one-shot client here: there is no server role and no client run, "
                            "so there is no control socket to call"},
        "request": None,
        "response": None,
        "elapsedMs": 0,
    })
    return EXIT_UNSUPPORTED


# ---- client ----


@dataclass
class Result:
    operation: str
    request: dict[str, Any] | None = None
    response: dict[str, Any] | None = None


@dataclass
class Options:
    edition: str
    url: Any
    tls_dir: Path
    fixture: Fixture
    connect_timeout: float
    timeout: float
    notify_listen: tuple[str, int]
    notify_host: str


def run_client(args: list[str]) -> int:
    if not args:
        raise UsageError("client needs an operation: " + ", ".join(CLIENT_OPERATIONS + ("run",)))
    op, args = args[0], args[1:]
    flags = parse_flags(args, "version", "url", "tls-dir", "fixture", "connect-timeout-ms", "timeout-ms", "payload",
                        "notify-listen", "notify-host", "ready-file", "control-socket")
    edition = edition_of(flags)
    url = urlsplit(required(flags, "url"))
    if url.scheme != "https" or not url.hostname:
        raise UsageError(f"--url {flags['url']!r} is not an https:// URL")
    connect_timeout = millis(flags, "connect-timeout-ms", 10.0)
    timeout = millis(flags, "timeout-ms", 30.0)
    if op not in CLIENT_OPERATIONS and op != "run":
        raise UsageError(f"unknown client operation {op!r}")
    if op != "run" and ("ready-file" in flags or "control-socket" in flags):
        raise UsageError("--ready-file and --control-socket are for client run")
    if "payload" in flags:
        try:
            json.loads(flags["payload"])
        except ValueError:
            raise UsageError(f"--payload is not JSON: {flags['payload']!r}") from None
    listen_host, listen_port = host_port(flags.get("notify-listen", DEFAULT_NOTIFY_LISTEN), "notify-listen", True)
    notify_host, notify_port = host_port(flags.get("notify-host", socket.gethostname()), "notify-host", False)
    notify_authority = flags.get("notify-host", "") if notify_port is not None else f"{notify_host}:{listen_port}"
    tls_dir = Path(required(flags, "tls-dir"))
    fixture = load_fixture(flags.get("fixture", str(DEFAULT_FIXTURE)))
    if op == "run":
        raise CodedError(EXIT_UNSUPPORTED, "client run is not implemented by the py20305 adapter")
    for name in ("client.crt", "client.key", "ca.crt"):
        if not (tls_dir / name).is_file():
            raise CodedError(EXIT_FIXTURE, f"--tls-dir {tls_dir} has no {name}")
    options = Options(edition, url, tls_dir, fixture, connect_timeout, timeout, (listen_host, listen_port),
                      notify_authority)

    result = Result(operation=op)
    start = time.monotonic()
    error: dict[str, str] | None = None
    code = EXIT_OK
    if op in NOT_IMPLEMENTED:
        error, code = {"code": "unsupported", "detail": NOT_IMPLEMENTED[op]}, EXIT_UNSUPPORTED
    else:
        try:
            asyncio.run(perform(op, options, result))
        except OperationError as exc:
            error = {"code": exc.code, "detail": str(exc)}
            code = {"connect-failed": EXIT_CONNECT, "unsupported": EXIT_UNSUPPORTED}.get(exc.code, EXIT_FAILED)
        except CodedError as exc:
            error, code = {"code": "invalid-request", "detail": str(exc)}, exc.code
    emit({
        "schemaVersion": "1.0",
        "adapter": ADAPTER,
        "version": edition,
        "operation": op,
        "ok": code == EXIT_OK,
        "error": error,
        "request": result.request,
        "response": result.response,
        "elapsedMs": int((time.monotonic() - start) * 1000),
    })
    return code


def status_recorder() -> Any:
    """A py20305 TrafficRecorder that remembers the status of the last write.

    Sep2Client.post returns only the Location, so the status of a POST is
    read from what py20305 records for its Live Traffic view."""
    from py20305.client.traffic_recorder import TrafficRecorder

    class StatusRecorder(TrafficRecorder):
        last_status: int | None = None

        def record_response(self, *, method: str, url: str, status: int | None = None, body: object = None,
                            error: str | None = None) -> None:
            self.last_status = status
            super().record_response(method=method, url=url, status=status, body=body, error=error)

    return StatusRecorder()


async def perform(op: str, options: Options, result: Result) -> None:
    from py20305.client.csip_client import CsipClient
    from py20305.client.errors import Sep2Error, Sep2ProtocolError
    from py20305.client.retry import RetryPolicy
    from py20305.client.tls import TlsConfig, create_ssl_context

    tls_dir, url = options.tls_dir, options.url
    tls = TlsConfig(client_cert=tls_dir / "client.crt", client_key=tls_dir / "client.key", ca_cert=tls_dir / "ca.crt")
    try:
        context = create_ssl_context(tls)
    except (OSError, ssl.SSLError, ValueError) as exc:
        raise OperationError("invalid-request", f"TLS material of {tls_dir}: {exc}") from None
    host, port = url.hostname, url.port or 443

    # The handshake on its own first, with py20305's own TLS context, so that
    # a server that cannot be reached or refuses the device is a connect
    # failure rather than an operation failure.
    try:
        _, writer = await asyncio.wait_for(
            asyncio.open_connection(host, port, ssl=context, server_hostname=host), options.connect_timeout)
        writer.close()
    except (OSError, ssl.SSLError, asyncio.TimeoutError) as exc:
        raise OperationError("connect-failed", f"{type(exc).__name__}: {exc}") from None

    client = CsipClient(
        f"https://{url.netloc}",
        tls=tls,
        retry=RetryPolicy(max_transient=0, max_tls=0),
        server_2018_compat=options.edition == "2018",
        dcap_path=url.path or "/dcap",
        heartbeat_enabled=False,
        reconcile_enabled=False,
        pricing_enabled=op == "poll-pricing",
    )
    client.http.traffic_recorder = status_recorder()
    operation = {"discover": op_discover, "register": op_register, "poll-controls": op_poll_controls,
                 "report": op_report, "subscribe": op_subscribe, "respond": op_respond, "log-event": op_log_event,
                 "put-der": op_put_der, "poll-pricing": op_poll_pricing}[op]
    try:
        await asyncio.wait_for(operation(client, options, result), options.timeout)
    except asyncio.TimeoutError:
        raise OperationError("timeout", f"no result within {options.timeout:g} s") from None
    except ssl.SSLError as exc:
        raise OperationError("connect-failed", f"{type(exc).__name__}: {exc}") from None
    except Sep2ProtocolError as exc:
        if result.response is None:
            result.response = {"status": exc.status_code}
        elif result.response.get("status") is None:
            result.response["status"] = exc.status_code
        raise OperationError("server-error", f"{type(exc).__name__}: {exc}") from None
    except (Sep2Error, OSError, ValueError) as exc:
        raise OperationError("server-error", f"{type(exc).__name__}: {exc}") from None
    finally:
        await client.http.close()


def last_status(client: Any) -> int | None:
    return client.http.traffic_recorder.last_status


def value_of(v: Any) -> Any:
    """The number inside a py20305 wrapper type (PerCent, PowerOfTenMultiplierType, ...)."""
    return getattr(v, "value", v)


def href_of(link: Any) -> str | None:
    return getattr(link, "href", None) if link is not None else None


async def op_discover(client: Any, options: Options, result: Result) -> None:
    from py20305.models.sep import DeviceCapability

    dcap_path = options.url.path or "/dcap"
    result.request = {"method": "GET", "path": dcap_path}
    dcap = await client.http.get(dcap_path, DeviceCapability)
    links = sorted(name for name, value in vars(dcap).items() if name.endswith("_link") and value is not None)
    result.response = {"status": 200, "body": {"href": dcap.href, "pollRate": dcap.poll_rate, "links": links}}


def own_end_device(client: Any) -> Any:
    """The discovered EndDevice of this client's certificate, or None."""
    lfdi = bytes.fromhex(client.http.client_lfdi)
    for edev in client.state.end_devices.values():
        if edev.lfdi == lfdi:
            return edev
    return None


async def connect(client: Any) -> Any:
    """CsipClient discovery; returns the device's own EndDevice state."""
    await client.connect()
    # Discovery detects CSIP-AUS (discovery.py) but sets it on the state
    # only, not on the HTTP client that serializes what is sent.
    client.http.csip_aus_mode = client.state.csip_aus_mode
    edev = own_end_device(client)
    if edev is None:
        raise OperationError("server-error", "the server does not list this device's EndDevice")
    return edev


async def op_register(client: Any, options: Options, result: Result) -> None:
    from py20305.models.sep import Registration

    fixture = options.fixture
    await client.connect()
    edev = own_end_device(client)
    created = False
    edev_list = client.state.dcap.end_device_list_link.href if client.state.dcap.end_device_list_link else None
    result.request = {"method": "GET", "path": edev_list}
    if edev is None:
        result.request = {"method": "POST", "path": edev_list}
        location = await client.register_end_device(lfdi=client.http.client_lfdi,
                                                    device_category=fixture.device_category)
        created = True
        await client.connect()
        edev = own_end_device(client)
        if edev is None:
            raise OperationError("server-error", f"the server did not list the EndDevice it created at {location}")
    body: dict[str, Any] = {"created": created, "endDevice": edev.href,
                            "lFDI": edev.lfdi.hex().upper(), "sFDI": edev.device.s_fdi.value}
    result.response = {"status": 201 if created else 200, "body": body}
    if not edev.registration_href:
        raise OperationError("server-error", "the server did not register the device: its EndDevice has no RegistrationLink")
    registration = await client.http.get(edev.registration_href, Registration)
    body["registrationPIN"] = registration.p_in.value
    if registration.p_in.value != fixture.pin:
        raise OperationError("server-error",
                             f"the Registration has PIN {registration.p_in.value}, the device was given {fixture.pin}")


# ---- DER programs, controls and curves ----


def power(value: int, multiplier: int) -> Fraction:
    return Fraction(value) * Fraction(10) ** multiplier


def curve_matches(want: dict[str, Any], curve: Any) -> bool:
    """A served DERCurve with the fixture curve's type, multipliers, yRefType and points."""
    if curve is None:
        return False
    points = [(p.xvalue, p.yvalue) for p in curve.curve_data]
    return (value_of(curve.curve_type) == want["curveType"]
            and value_of(curve.x_multiplier) == want["xMultiplier"]
            and value_of(curve.y_multiplier) == want["yMultiplier"]
            and value_of(curve.y_ref_type) == want.get("yRefType", 0)
            and points == [(p["x"], p["y"]) for p in want["points"]])


@dataclass
class ServedProgram:
    """What the server serves under one program description."""

    description: str
    primacy: int | None
    defaults: list[Any] = field(default_factory=list)  # DefaultDERControls
    controls: list[Any] = field(default_factory=list)  # DERControls
    curves: dict[str, Any] = field(default_factory=dict)  # DERCurves by href


async def served_programs(client: Any) -> dict[str, ServedProgram]:
    """The discovered programs by description, with the curves their controls link."""
    from py20305.models.sep import Dercurve

    served: dict[str, ServedProgram] = {}
    for program in client.state.der_programs.values():
        description = program.program.description or ""
        sp = served.setdefault(description, ServedProgram(description, program.primacy))
        if program.default_dercontrol is not None:
            sp.defaults.append(program.default_dercontrol)
        sp.controls.extend(program.der_controls)
        for curve in program.der_curves:
            if curve.href:
                sp.curves[curve.href] = curve
    # A curve a control links to, but that the DERCurveList did not hold.
    for sp in served.values():
        for base in [c.dercontrol_base for c in sp.controls + sp.defaults]:
            for attr in CURVE_MODES.values():
                href = href_of(getattr(base, attr, None)) if base is not None else None
                if href and href not in sp.curves:
                    sp.curves[href] = await client.http.get(href, Dercurve)
    return served


def base_matches(want: dict[str, Any], base: Any, program: dict[str, Any], sp: ServedProgram) -> bool:
    """Whether a served DERControlBase holds exactly the fixture base's elements and values."""
    from py20305.models.csipaus.elements import doe_limits

    if base is None:
        return False
    got: dict[str, Any] = {name: value_of(getattr(base, attr)) for name, attr in PLAIN_MODES.items()
                           if getattr(base, attr, None) is not None}
    for name, attr in CURVE_MODES.items():
        link = getattr(base, attr, None)
        if link is not None:
            got[name] = href_of(link)
    for name, (value, multiplier) in doe_limits(base).items():
        got[name] = power(value, multiplier)
    if set(got) != set(want):
        return False
    curves = {c["mRID"]: c for c in program.get("curves", [])}
    for name, wanted in want.items():
        if name in LIMIT_MODES:
            if got[name] != power(wanted["value"], wanted["multiplier"]):
                return False
        elif name in CURVE_MODES:
            if not curve_matches(curves[wanted], sp.curves.get(got[name])):
                return False
        elif got[name] != wanted or type(got[name]) is not type(wanted):
            return False
    return True


def control_matches(control: dict[str, Any], served: Any, program: dict[str, Any], sp: ServedProgram) -> bool:
    """Whether a served DERControl has the fixture control's duration and DERControlBase values."""
    interval = served.interval
    if interval is None or interval.duration != control["duration"]:
        return False
    return base_matches(control["base"], served.dercontrol_base, program, sp)


def describe_base(base: Any) -> dict[str, Any] | None:
    """A served DERControlBase as JSON, for the result document."""
    from py20305.models.csipaus.elements import doe_limits

    if base is None:
        return None
    out: dict[str, Any] = {name: value_of(getattr(base, attr)) for name, attr in PLAIN_MODES.items()
                           if getattr(base, attr, None) is not None}
    out.update({name: href_of(getattr(base, attr)) for name, attr in CURVE_MODES.items()
                if getattr(base, attr, None) is not None})
    out.update({name: {"value": v, "multiplier": m} for name, (v, m) in doe_limits(base).items()})
    return out


def check_programs(fixture: Fixture, served: dict[str, ServedProgram]) -> None:
    """Every program of the fixture, with its default control, controls and curves, is served."""
    for program in fixture.der_programs:
        name = program["description"]
        sp = served.get(name)
        if sp is None:
            raise OperationError("server-error", f"the server does not serve the fixture's DER program {name!r}")
        if "defaultControl" in program and not any(
                base_matches(program["defaultControl"]["base"], d.dercontrol_base, program, sp) for d in sp.defaults):
            raise OperationError("server-error", f"the server does not serve the default control "
                                                 f"{program['defaultControl']['description']!r} of {name!r}")
        for control in program["controls"]:
            if not any(control_matches(control, dc, program, sp) for dc in sp.controls):
                raise OperationError("server-error", f"the server does not serve the fixture's control "
                                                     f"{control['description']!r} of {name!r}")
        for curve in program.get("curves", []):
            if not any(curve_matches(curve, c) for c in sp.curves.values()):
                raise OperationError("server-error", f"the server does not serve the fixture's curve "
                                                     f"{curve['description']!r} of {name!r}")


async def op_poll_controls(client: Any, options: Options, result: Result) -> None:
    edev = await connect(client)
    fsa_link = edev.device.function_set_assignments_list_link
    result.request = {"method": "GET", "path": href_of(fsa_link)}
    served = await served_programs(client)
    result.response = {"status": 200, "body": {"derPrograms": [{
        "description": sp.description,
        "primacy": sp.primacy,
        "defaultControls": [describe_base(d.dercontrol_base) for d in sp.defaults],
        "controls": [{"description": c.description, "duration": c.interval.duration if c.interval else None,
                      "base": describe_base(c.dercontrol_base)} for c in sp.controls],
        "curves": [{"href": href, "curveType": value_of(c.curve_type)} for href, c in sp.curves.items()],
    } for sp in served.values()]}}
    if not served:
        raise OperationError("server-error", "the device's function set assignments hold no DER program")
    # The server was seeded with the fixture's programs and controls. A server
    # picks its own mRIDs and may describe controls its own way, so programs
    # are matched by description and controls by what they do.
    check_programs(options.fixture, served)


async def op_respond(client: Any, options: Options, result: Result) -> None:
    from py20305.events.modes_bitmask import build_modes_responded
    from py20305.models.sep import DercontrolResponse, MRidtype, TimeType

    await connect(client)
    served = await served_programs(client)
    target = None
    for program in options.fixture.der_programs:
        for control in program["controls"]:
            if control.get("start", 0) == 0:
                target = (program, control)
                break
        if target:
            break
    if target is None:
        raise OperationError("invalid-request", "the fixture has no control that is active now (start 0)")
    program, control = target
    sp = served.get(program["description"])
    match = next((dc for dc in sp.controls if control_matches(control, dc, program, sp)), None) if sp else None
    if match is None:
        raise OperationError("server-error", f"the server does not serve the fixture's control "
                                             f"{control['description']!r} of {program['description']!r}")
    if not match.reply_to:
        raise OperationError("server-error", f"the served control {match.href} has no replyTo")
    response = DercontrolResponse(
        end_device_lfdi=bytes.fromhex(client.http.client_lfdi),
        status=2,  # event started
        subject=MRidtype(value=match.m_rid.value),
        created_date_time=TimeType(value=int(time.time())),
        # modesResponded is IEEE 2030.5-2023; py20305 omits it for 2018 servers too.
        modes_responded=None if client.http.server_2018_compat else build_modes_responded(match.dercontrol_base),
    )
    result.request = {"method": "POST", "path": match.reply_to}
    location = await client.http.post(match.reply_to, response)
    status = last_status(client)
    result.response = {"status": status, "body": {"control": match.href, "subject": match.m_rid.value.hex().upper(),
                                                  "location": location}}
    if status not in (201, 204):
        raise OperationError("server-error", f"the server answered the DERControlResponse with {status}, "
                                             "not 201 or 204")


async def op_log_event(client: Any, options: Options, result: Result) -> None:
    from py20305.models.sep import LogEvent, Pentype, TimeType

    want = options.fixture.log_event
    if want is None:
        raise OperationError("invalid-request", "the fixture has no logEvent")
    edev = await connect(client)
    if not edev.log_event_list_href:
        raise OperationError("server-error", "the device's EndDevice has no LogEventListLink")
    event = LogEvent(
        created_date_time=TimeType(value=int(time.time())),
        details=want["details"],
        function_set=want["functionSet"],
        log_event_code=want["logEventCode"],
        log_event_id=want["logEventID"],
        log_event_pen=Pentype(value=want["logEventPEN"]),
        profile_id=want["profileID"],
    )
    result.request = {"method": "POST", "path": edev.log_event_list_href}
    location = await client.http.post(edev.log_event_list_href, event)
    status = last_status(client)
    result.response = {"status": status, "body": {"location": location}}
    if status != 201:
        raise OperationError("server-error", f"the server answered the LogEvent with {status}, not 201")


async def op_put_der(client: Any, options: Options, result: Result) -> None:
    from py20305.models.sep import (
        ActivePower, Dercapability, DercontrolType, Dersettings, Dertype, PowerOfTenMultiplierType, TimeType,
    )
    from py20305.telemetry.der_availability import build_der_availability
    from py20305.telemetry.der_status import build_der_status

    der = options.fixture.der
    if der is None:
        raise OperationError("invalid-request", "the fixture has no der")
    edev = await connect(client)
    if not edev.ders:
        raise OperationError("server-error", "the device's EndDevice has no DER in a DERList")

    def active(p: dict[str, int]) -> Any:
        return ActivePower(multiplier=PowerOfTenMultiplierType(value=p["multiplier"]), value=p["value"])

    now = int(time.time())
    cap, settings, status, avail = der["capability"], der["settings"], der["status"], der["availability"]
    # The CSIP-AUS doeModesSupported and doeModesEnabled extensions, when the
    # fixture has them, as py20305 sends them in CSIP-AUS mode
    # (telemetry/der_capability.py, der_settings.py): its models, and the
    # csipaus namespace declared by Sep2Client.put with csip_aus_mode.
    cap_ext: list[Any] = []
    settings_ext: list[Any] = []
    if "doeModesSupported" in cap or "doeModesEnabled" in settings:
        from py20305.models.csipaus.csipaus_ext import DoeModesEnabled, DoeModesSupported

        client.http.csip_aus_mode = True
        if "doeModesSupported" in cap:
            cap_ext.append(DoeModesSupported(value=bytes.fromhex(cap["doeModesSupported"])))
        if "doeModesEnabled" in settings:
            settings_ext.append(DoeModesEnabled(value=bytes.fromhex(settings["doeModesEnabled"])))
    resources = [
        ("DERCapability", edev.der_capability_href, Dercapability(
            modes_supported=DercontrolType(value=bytes.fromhex(cap["modesSupported"])),
            rtg_max_w=active(cap["rtgMaxW"]),
            type_value=Dertype(value=cap["type"]),
            other_element=cap_ext)),
        ("DERSettings", edev.der_settings_href, Dersettings(
            modes_enabled=DercontrolType(value=bytes.fromhex(settings["modesEnabled"])),
            set_max_w=active(settings["setMaxW"]),
            set_grad_w=settings["setGradW"],
            updated_time=TimeType(value=now),
            other_element=settings_ext)),
        # Under 2018 compatibility py20305 sends genConnectStatus, under 2023
        # connectStatus with it (telemetry/der_status.py).
        ("DERStatus", edev.der_status_href, build_der_status(
            {"genConnectStatus": {"value": int(status["genConnectStatus"], 16)},
             "operationalModeStatus": {"value": status["operationalModeStatus"]}},
            server_2018_compat=client.http.server_2018_compat, default_time=now)),
        ("DERAvailability", edev.der_availability_href, build_der_availability(
            {"availabilityDuration": avail["availabilityDuration"], "statWAvail": avail["statWAvail"]},
            default_time=now)),
    ]
    puts: list[dict[str, Any]] = []
    result.response = {"status": None, "body": {"puts": puts}}
    for name, href, model in resources:
        if not href:
            raise OperationError("server-error", f"the device's DER has no {name}Link")
        result.request = {"method": "PUT", "path": href}
        code = await client.http.put(href, model)
        puts.append({"resource": name, "path": href, "status": code})
        result.response["status"] = code
        if code not in (200, 204):
            raise OperationError("server-error", f"the server answered the {name} PUT with {code}")


# ---- pricing ----


async def op_poll_pricing(client: Any, options: Options, result: Result) -> None:
    from py20305.client import discovery
    from py20305.models.sep import ConsumptionTariffIntervalList

    edev = await connect(client)
    result.request = {"method": "GET", "path": href_of(edev.device.function_set_assignments_list_link)}
    state = client.state
    if not state.tariff_profiles and href_of(state.dcap.tariff_profile_list_link):
        # py20305 follows the TariffProfileListLinks of the FunctionSetAssignments
        # only; the DeviceCapability's is the contract's fallback, walked with
        # the same discovery step.
        result.request = {"method": "GET", "path": href_of(state.dcap.tariff_profile_list_link)}
        await discovery._discover_tariffs_for_fsa(client.http, state.dcap, state)

    first_prices: dict[str, int | None] = {}

    async def first_price(tti: Any) -> int | None:
        href = href_of(tti.consumption_tariff_interval_list_link)
        if not href:
            return None
        if href not in first_prices:
            pages = await client.http.get_list(href, ConsumptionTariffIntervalList)
            ctis = [cti for page in pages for cti in page.consumption_tariff_interval]
            first_prices[href] = ctis[0].price if ctis else None
        return first_prices[href]

    served = []
    for tp in state.tariff_profiles.values():
        components = []
        for rc in tp.rate_components:
            intervals = [{"duration": tti.interval.duration if tti.interval else None,
                          "price": await first_price(tti)} for tti in rc.time_tariff_intervals]
            components.append({"description": rc.rate_component.description, "intervals": intervals})
        served.append({"description": tp.profile.description, "currency": value_of(tp.profile.currency),
                       "pricePowerOfTenMultiplier": value_of(tp.profile.price_power_of_ten_multiplier),
                       "rateComponents": components})
    result.response = {"status": 200, "body": {"tariffs": served}}
    for tariff in options.fixture.tariffs:
        candidates = [t for t in served if t["description"] == tariff["description"]
                      and t["currency"] == tariff["currency"]
                      and t["pricePowerOfTenMultiplier"] == tariff["pricePowerOfTenMultiplier"]]
        if not candidates:
            raise OperationError("server-error", f"the server does not serve the fixture's tariff "
                                                 f"{tariff['description']!r} (currency {tariff['currency']}, "
                                                 f"multiplier {tariff['pricePowerOfTenMultiplier']})")
        for rc in tariff["rateComponents"]:
            comps = [c for t in candidates for c in t["rateComponents"] if c["description"] == rc["description"]]
            if not comps:
                raise OperationError("server-error", f"the server does not serve the rate component "
                                                     f"{rc['description']!r} of {tariff['description']!r}")
            for interval in rc["timeTariffIntervals"]:
                want = {"duration": interval["duration"], "price": interval["price"]}
                if not any(want in c["intervals"] for c in comps):
                    raise OperationError("server-error", f"the server does not serve the interval "
                                                         f"{interval['description']!r} of {rc['description']!r}")


# ---- metering ----


async def op_report(client: Any, options: Options, result: Result) -> None:
    from py20305.models.sep import (
        AccumulationBehaviourType, CommodityType, DataQualifierType, DateTimeInterval, FlowDirectionType,
        KindType, MirrorMeterReading1, MirrorUsagePoint, MRidtype, PhaseCode, PowerOfTenMultiplierType,
        Reading1, ReadingType1, RoleFlagsType, ServiceKind, TimeType, UomType,
    )

    fixture = options.fixture
    await client.connect()
    href = client.state.mup_list_href
    if not href:
        raise OperationError("server-error", "the DeviceCapability has no MirrorUsagePointListLink")
    result.request = {"method": "POST", "path": href}
    now = int(time.time())
    readings = []
    for r in fixture.report["meterReadings"]:
        rt = r["readingType"]
        typed = {
            "accumulation_behaviour": (AccumulationBehaviourType, "accumulationBehaviour"),
            "commodity": (CommodityType, "commodity"),
            "data_qualifier": (DataQualifierType, "dataQualifier"),
            "flow_direction": (FlowDirectionType, "flowDirection"),
            "kind": (KindType, "kind"),
            "phase": (PhaseCode, "phase"),
            "power_of_ten_multiplier": (PowerOfTenMultiplierType, "powerOfTenMultiplier"),
            "uom": (UomType, "uom"),
        }
        reading_type = ReadingType1(**{attr: kind(value=rt[key]) for attr, (kind, key) in typed.items() if key in rt})
        readings.append(MirrorMeterReading1(
            m_rid=MRidtype(value=bytes.fromhex(r["mRID"])),
            description=r["description"],
            reading_type=reading_type,
            reading=Reading1(time_period=DateTimeInterval(duration=0, start=TimeType(value=now)), value=r["value"]),
            last_update_time=TimeType(value=now),
        ))
    m = fixture.report["mirrorUsagePoint"]
    mup = MirrorUsagePoint(
        m_rid=MRidtype(value=bytes.fromhex(m["mRID"])),
        description=m["description"],
        role_flags=RoleFlagsType(value=bytes.fromhex(m["roleFlags"])),
        service_category_kind=ServiceKind(value=m["serviceCategoryKind"]),
        status=m["status"],
        device_lfdi=bytes.fromhex(client.http.client_lfdi),
        mirror_meter_reading=readings,
    )
    location = await client.http.post(href, mup)
    result.response = {"status": last_status(client), "body": {"location": location}}
    if not location:
        raise OperationError("server-error", "the server did not answer the MirrorUsagePoint with a Location")


# ---- subscriptions ----


async def op_subscribe(client: Any, options: Options, result: Result) -> None:
    from py20305.client.tls import TlsConfig
    from py20305.models.sep import Subscription
    from py20305.subscription.notification_server import NotificationServer

    tls_dir = options.tls_dir
    if (tls_dir / "notify.crt").is_file() and (tls_dir / "notify.key").is_file():
        cert, key = tls_dir / "notify.crt", tls_dir / "notify.key"
    else:
        cert, key = tls_dir / "client.crt", tls_dir / "client.key"
    # NotificationServer serves TlsConfig.client_cert as its own certificate.
    listener_tls = TlsConfig(client_cert=cert, client_key=key, ca_cert=tls_dir / "ca.crt")
    arrived: asyncio.Queue[Any] = asyncio.Queue()

    async def on_notification(notification: Any) -> None:
        await arrived.put(notification)

    # warn: a server need not present a certificate when it delivers.
    server = NotificationServer(host=options.notify_listen[0], port=options.notify_listen[1], tls=listener_tls,
                                on_notification=on_notification, client_cert_mode="warn")
    try:
        await server.start()
    except OSError as exc:
        raise OperationError("invalid-request", f"cannot listen on {options.notify_listen[0]}:"
                                                f"{options.notify_listen[1]}: {exc}") from None
    try:
        edev = await connect(client)
        if not edev.subscription_list_href:
            raise OperationError("server-error", "the device's EndDevice has no SubscriptionListLink")
        if not edev.fsa_list or not href_of(edev.fsa_list[0].derprogram_list_link):
            raise OperationError("server-error", "the device's first FunctionSetAssignments has no DERProgramListLink")
        resource = href_of(edev.fsa_list[0].derprogram_list_link)
        notification_uri = f"https://{options.notify_host}/notify"
        subscription = Subscription(subscribed_resource=resource, encoding=0, level="+S1", limit=1,
                                    notification_uri=notification_uri)
        result.request = {"method": "POST", "path": edev.subscription_list_href}
        location = await client.http.post(edev.subscription_list_href, subscription)
        status = last_status(client)
        body: dict[str, Any] = {"location": location, "subscribedResource": resource,
                                "notificationURI": notification_uri, "notification": None}
        result.response = {"status": status, "body": body}
        if status != 201:
            raise OperationError("server-error", f"the server answered the Subscription with {status}, not 201")
        notification = await arrived.get()
        body["notification"] = {
            "subscribedResource": notification.subscribed_resource,
            "subscriptionURI": notification.subscription_uri,
            "status": value_of(notification.status),
            "resource": type(notification.resource).__name__ if notification.resource is not None else None,
        }
        if value_of(notification.status) != 0:
            raise OperationError("server-error", f"the Notification has status {value_of(notification.status)}, "
                                                 "not 0 (default)")
    finally:
        await server.stop()


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

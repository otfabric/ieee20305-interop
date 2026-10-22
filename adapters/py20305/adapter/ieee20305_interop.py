# SPDX-License-Identifier: MIT
"""ieee20305-interop adapter for py20305, the DER Security IEEE 2030.5 client.

Drives the py20305 protocol engine (CsipClient, its discovery and its
models) behind the commands of docs/CONTAINER_CONTRACT.md. py20305 is a
client only: the server command validates its flags and exits 5.
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import ssl
import sys
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

EXIT_OK, EXIT_FAILED, EXIT_USAGE, EXIT_CONNECT, EXIT_FIXTURE, EXIT_UNSUPPORTED = 0, 1, 2, 3, 4, 5

ADAPTER = "py20305"
CONTRACT_VERSION = "0.2"
EDITIONS = ("2018", "2023")
OPERATIONS = ("register", "discover", "poll-controls", "report")
FIXTURES = Path("/fixtures")
DEFAULT_FIXTURE = FIXTURES / "baseline" / "fixture.json"

# Set by the Dockerfile.
ADAPTER_VERSION = os.environ.get("IEEE20305_INTEROP_ADAPTER_VERSION", "dev")
UPSTREAM_VERSION = os.environ.get("IEEE20305_INTEROP_UPSTREAM_VERSION", "unknown")
UPSTREAM_REVISION = os.environ.get("IEEE20305_INTEROP_UPSTREAM_REVISION", "unknown")

# What py20305 offers (py20305.client.tls): TLS 1.2 with these suites only.
TLS_VERSIONS = ["1.2"]
CIPHER_SUITES = ["TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384", "TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8"]


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
            raise CodedError(EXIT_UNSUPPORTED, "call: the control socket is not implemented by the py20305 adapter")
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


# ---- capabilities and fixtures ----


def capabilities() -> dict[str, Any]:
    notes = [
        "TLS: TLS 1.2 with TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384 and TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8, "
        "through Python's ssl module (OpenSSL), as py20305 configures it.",
        "Client only: py20305 has no server role.",
        "Verifies the server's host name against its certificate (TlsConfig.check_hostname, on by default), "
        "so a server certificate must name the host the client connects to.",
        "Operations run on the py20305 protocol engine: CsipClient discovery and registration, "
        "and its IEEE 2030.5 models for the MirrorUsagePoint.",
        "Subscriptions and the control socket are not implemented by this adapter yet.",
    ]
    role_client = {"supported": True, "operations": list(OPERATIONS)}
    role_server = {"supported": False, "operations": []}
    features = {"mTLS": True, "csip": True, "csipAus": False, "subscription": False}
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
        "versions": {
            edition: {"client": role_client, "server": role_server, "features": features, "notes": notes}
            for edition in EDITIONS
        },
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
        )
        for program in fixture.der_programs:
            program["description"], [c["description"] for c in program["controls"]]
        fixture.report["mirrorUsagePoint"]["mRID"], fixture.report["meterReadings"][0]["readingType"]
    except (OSError, ValueError, KeyError, TypeError, IndexError) as exc:
        raise CodedError(EXIT_FIXTURE, f"fixture {path}: {exc!r}") from None
    return fixture


# ---- client ----


@dataclass
class Result:
    operation: str
    request: dict[str, Any] | None = None
    response: dict[str, Any] | None = None


def run_client(args: list[str]) -> int:
    if not args:
        raise UsageError("client needs an operation: " + ", ".join(OPERATIONS + ("subscribe", "run")))
    op, args = args[0], args[1:]
    flags = parse_flags(args, "version", "url", "tls-dir", "fixture", "connect-timeout-ms", "timeout-ms", "payload")
    edition = edition_of(flags)
    url = urlsplit(required(flags, "url"))
    if url.scheme != "https" or not url.hostname:
        raise UsageError(f"--url {flags['url']!r} is not an https:// URL")
    connect_timeout = millis(flags, "connect-timeout-ms", 10.0)
    timeout = millis(flags, "timeout-ms", 30.0)
    if op not in OPERATIONS and op not in ("subscribe", "run"):
        raise UsageError(f"unknown client operation {op!r}")
    tls_dir = Path(required(flags, "tls-dir"))
    fixture = load_fixture(flags.get("fixture", str(DEFAULT_FIXTURE)))
    if op == "run":
        raise CodedError(EXIT_UNSUPPORTED, "client run is not implemented by the py20305 adapter")
    for name in ("client.crt", "client.key", "ca.crt"):
        if not (tls_dir / name).is_file():
            raise CodedError(EXIT_FIXTURE, f"--tls-dir {tls_dir} has no {name}")

    result = Result(operation=op)
    start = time.monotonic()
    error: dict[str, str] | None = None
    code = EXIT_OK
    if op == "subscribe":
        error, code = {"code": "unsupported", "detail": "subscriptions are not implemented by the py20305 adapter"}, EXIT_UNSUPPORTED
    else:
        try:
            asyncio.run(perform(op, edition, url, tls_dir, fixture, connect_timeout, timeout, result))
        except OperationError as exc:
            error = {"code": exc.code, "detail": str(exc)}
            code = EXIT_CONNECT if exc.code == "connect-failed" else EXIT_FAILED
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


async def perform(op: str, edition: str, url: Any, tls_dir: Path, fixture: Fixture,
                  connect_timeout: float, timeout: float, result: Result) -> None:
    from py20305.client.csip_client import CsipClient
    from py20305.client.errors import Sep2Error, Sep2ProtocolError
    from py20305.client.retry import RetryPolicy
    from py20305.client.tls import TlsConfig, create_ssl_context

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
            asyncio.open_connection(host, port, ssl=context, server_hostname=host), connect_timeout)
        writer.close()
    except (OSError, ssl.SSLError, asyncio.TimeoutError) as exc:
        raise OperationError("connect-failed", f"{type(exc).__name__}: {exc}") from None

    client = CsipClient(
        f"https://{url.netloc}",
        tls=tls,
        retry=RetryPolicy(max_transient=0, max_tls=0),
        server_2018_compat=edition == "2018",
        dcap_path=url.path or "/dcap",
        heartbeat_enabled=False,
        reconcile_enabled=False,
    )
    operation = {"discover": op_discover, "register": op_register,
                 "poll-controls": op_poll_controls, "report": op_report}[op]
    try:
        await asyncio.wait_for(operation(client, fixture, result, url.path or "/dcap"), timeout)
    except asyncio.TimeoutError:
        raise OperationError("timeout", f"no result within {timeout:g} s") from None
    except ssl.SSLError as exc:
        raise OperationError("connect-failed", f"{type(exc).__name__}: {exc}") from None
    except Sep2ProtocolError as exc:
        raise OperationError("server-error", f"{type(exc).__name__}: {exc}") from None
    except (Sep2Error, OSError, ValueError) as exc:
        raise OperationError("server-error", f"{type(exc).__name__}: {exc}") from None
    finally:
        await client.http.close()


async def op_discover(client: Any, _: Fixture, result: Result, dcap_path: str) -> None:
    from py20305.models.sep import DeviceCapability

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


async def op_register(client: Any, fixture: Fixture, result: Result, dcap_path: str) -> None:
    from py20305.models.sep import Registration

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


async def op_poll_controls(client: Any, fixture: Fixture, result: Result, dcap_path: str) -> None:
    await client.connect()
    edev = own_end_device(client)
    if edev is None:
        raise OperationError("server-error", "the server does not list this device's EndDevice")
    fsa_link = edev.device.function_set_assignments_list_link
    result.request = {"method": "GET", "path": fsa_link.href if fsa_link else None}
    programs = []
    served: set[tuple[str, str | None]] = set()
    for program in client.state.der_programs.values():
        description = program.program.description
        controls = [c.description for c in program.der_controls]
        served.add((description, None))
        served.update((description, c) for c in controls)
        programs.append({
            "description": description,
            "primacy": program.primacy,
            "defaultControl": ({"description": program.default_dercontrol.description}
                               if program.default_dercontrol else None),
            "controls": controls,
        })
    result.response = {"status": 200, "body": {"derPrograms": programs}}
    if not programs:
        raise OperationError("server-error", "the device's function set assignments hold no DER program")
    # The server was seeded with the fixture's programs and controls.
    for program in fixture.der_programs:
        if (program["description"], None) not in served:
            raise OperationError("server-error", f"the server does not serve the fixture's DER program {program['description']!r}")
        for control in program["controls"]:
            if (program["description"], control["description"]) not in served:
                raise OperationError("server-error",
                                     f"the server does not serve the fixture's control {control['description']!r} "
                                     f"of {program['description']!r}")


async def op_report(client: Any, fixture: Fixture, result: Result, dcap_path: str) -> None:
    from py20305.models.sep import (
        AccumulationBehaviourType, CommodityType, DataQualifierType, DateTimeInterval, FlowDirectionType,
        KindType, MirrorMeterReading1, MirrorUsagePoint, MRidtype, PhaseCode, PowerOfTenMultiplierType,
        Reading1, ReadingType1, RoleFlagsType, ServiceKind, TimeType, UomType,
    )

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
    result.response = {"status": 201, "body": {"location": location}}
    if not location:
        raise OperationError("server-error", "the server did not answer the MirrorUsagePoint with a Location")


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

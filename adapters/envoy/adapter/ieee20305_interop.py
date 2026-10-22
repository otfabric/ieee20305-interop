# SPDX-License-Identifier: MIT
"""ieee20305-interop adapter for Envoy, the ANU IEEE 2030.5 / CSIP-AUS server.

Runs Envoy as its own deployment does: PostgreSQL migrated by Envoy's
Alembic scripts, the Envoy server and admin applications under uvicorn, and
a TLS proxy (HAProxy) that terminates mutual TLS and forwards the client
certificate's SHA-256 fingerprint in the header Envoy reads. All of it runs
in this container, as uid 10001, on loopback except the proxy.

Devices are provisioned the way Envoy's own demo seeds its database: sites
inserted through Envoy's CRUD layer. Programs and controls go through
Envoy's admin API. Envoy is a server only: the client command validates its
flags and exits 5.
"""

from __future__ import annotations

import asyncio
import base64
import hashlib
import json
import os
import secrets
import signal
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

EXIT_OK, EXIT_FAILED, EXIT_USAGE, EXIT_CONNECT, EXIT_FIXTURE, EXIT_UNSUPPORTED = 0, 1, 2, 3, 4, 5

ADAPTER = "envoy"
CONTRACT_VERSION = "0.2"
EDITIONS = ("2018", "2023")  # what --version accepts
SERVED_EDITIONS = ("2018",)  # what Envoy serves
OPERATIONS = ("register", "discover", "poll-controls", "report")
FIXTURES = Path("/fixtures")
DEFAULT_FIXTURE = FIXTURES / "baseline" / "fixture.json"
DEFAULT_READY = "/run/ieee20305-interop/ready"

ADAPTER_VERSION = os.environ.get("IEEE20305_INTEROP_ADAPTER_VERSION", "dev")
UPSTREAM_VERSION = os.environ.get("IEEE20305_INTEROP_UPSTREAM_VERSION", "unknown")
UPSTREAM_REVISION = os.environ.get("IEEE20305_INTEROP_UPSTREAM_REVISION", "unknown")

VENV = Path("/opt/venv/bin")
ALEMBIC_DIR = Path("/opt/envoy")
WORK = Path("/tmp/ieee20305-interop")
PG_PORT, SERVER_PORT, ADMIN_PORT = 5432, 8000, 8001
SITE_GROUP = "ieee20305-interop"

# What the TLS proxy offers: TLS 1.2, the cipher suite IEEE 2030.5 mandates,
# and the two ECDSA GCM suites Envoy's own nginx example offers beside it.
CIPHERS_OPENSSL = "ECDHE-ECDSA-AES128-CCM8:ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:@SECLEVEL=0"
CIPHER_SUITES = [
    "TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8",
    "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
    "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
]

# DERControlBase elements, by their IEEE 2030.5 names, and the field of
# Envoy's admin SiteControlRequest that issues them.
CONTROL_FIELDS = {"opModConnect": "set_connect", "opModEnergize": "set_energized"}


class UsageError(Exception):
    """A malformed invocation: exit 2."""


class CodedError(Exception):
    """Ends the command with a contract exit code."""

    def __init__(self, code: int, message: str) -> None:
        super().__init__(message)
        self.code = code


def main(argv: list[str]) -> int:
    if not argv:
        print("usage: ieee20305-interop server|client|call|print-capabilities|print-fixture|version", file=sys.stderr)
        return EXIT_USAGE
    command, args = argv[0], argv[1:]
    try:
        if command == "server":
            return run_server(args)
        if command == "client":
            return run_client(args)
        if command == "call":
            raise CodedError(EXIT_UNSUPPORTED, "call: the control socket is not implemented by the envoy adapter")
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
            print(f"ieee20305-interop {ADAPTER} {ADAPTER_VERSION} (envoy {UPSTREAM_VERSION})")
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


def event(name: str, **fields: Any) -> None:
    emit({"event": name, "time": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), **fields})


def log(message: str) -> None:
    print(f"ieee20305-interop: {message}", file=sys.stderr, flush=True)


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


def positive_int(flags: dict[str, str], name: str, default: int, maximum: int) -> int:
    if name not in flags:
        return default
    try:
        value = int(flags[name])
    except ValueError:
        value = 0
    if not 0 < value <= maximum:
        raise UsageError(f"--{name} {flags[name]!r} is not a number from 1 to {maximum}")
    return value


# ---- capabilities and fixtures ----


def capabilities() -> dict[str, Any]:
    notes = [
        "Server only: Envoy has no client role.",
        "Deployment: PostgreSQL, Envoy's server and admin applications under uvicorn, and HAProxy, which "
        "terminates mutual TLS (TLS 1.2; CCM_8 and the ECDSA AES-GCM suites of Envoy's own nginx example) and "
        "forwards the client certificate's SHA-256 fingerprint in x-forwarded-client-cert.",
        "Devices of --tls-dir/devices are provisioned as sites of Envoy's NULL aggregator through Envoy's CRUD "
        "layer, as Envoy's demo seeds its database; device registration is on (ALLOW_DEVICE_REGISTRATION), so a "
        "device that was not provisioned can register itself, with a PIN Envoy chooses.",
        "Programs and controls are issued through Envoy's admin API: a program is a site control group, a control "
        "a site control for a site group of the provisioned devices. Envoy describes a control by its start time.",
        "Fixtures: DERControlBase opModConnect and opModEnergize only (Envoy's CSIP-AUS limits have no base IEEE "
        "2030.5 equivalent in the fixtures), and no defaultControl (Envoy's defaults are CSIP-AUS limits only).",
        "Serves IEEE 2030.5-2018, the edition CSIP-AUS is based on.",
        "Subscriptions and the control socket are not implemented by this adapter yet.",
    ]
    server = {"supported": True, "operations": list(OPERATIONS)}
    client = {"supported": False, "operations": []}
    features = {"mTLS": True, "csip": True, "csipAus": False, "subscription": False}
    return {
        "schemaVersion": "1.0",
        "adapter": ADAPTER,
        "adapterVersion": ADAPTER_VERSION,
        "contractVersion": CONTRACT_VERSION,
        "upstream": {
            "name": "ANU Envoy",
            "repository": "https://github.com/bsgip/envoy",
            "version": UPSTREAM_VERSION,
            "revision": UPSTREAM_REVISION,
            "license": "MIT",
            "language": "Python",
            "independentEngine": True,
        },
        "tls": {"versions": ["1.2"], "cipherSuites": CIPHER_SUITES},
        "versions": {
            edition: {"client": client, "server": server, "features": features, "notes": notes}
            for edition in SERVED_EDITIONS
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
        fixture["name"], fixture["device"]["pin"], fixture["device"]["deviceCategory"]
        for program in fixture["derPrograms"]:
            program["description"], program["primacy"]
            for control in program["controls"]:
                control["base"], control["duration"]
    except (OSError, ValueError, KeyError, TypeError) as exc:
        raise CodedError(EXIT_FIXTURE, f"fixture {path}: {exc!r}") from None
    return fixture


def check_fixture(fixture: dict[str, Any]) -> None:
    """Exit 5 for what Envoy cannot be seeded with: never a silent subset."""
    pin = fixture["device"]["pin"]
    raw = pin // 10
    # Envoy stores a five-digit PIN and serves it with a check digit of its
    # own: the digit sum of the five digits, modulo 10.
    if raw * 10 + sum(int(d) for d in str(raw)) % 10 != pin:
        raise CodedError(EXIT_UNSUPPORTED, f"Envoy cannot serve the registration PIN {pin}: it would serve "
                                           f"{raw * 10 + sum(int(d) for d in str(raw)) % 10} for the digits {raw}")
    for program in fixture["derPrograms"]:
        if "defaultControl" in program:
            raise CodedError(EXIT_UNSUPPORTED, f"program {program['description']!r}: Envoy's default controls are "
                                               "CSIP-AUS limits only and cannot hold a fixture's defaultControl")
        for control in program["controls"]:
            for name in control["base"]:
                if name not in CONTROL_FIELDS:
                    raise CodedError(EXIT_UNSUPPORTED, f"control {control['description']!r}: Envoy cannot issue "
                                                       f"the DERControlBase element {name}")


# ---- client ----


def run_client(args: list[str]) -> int:
    if not args:
        raise UsageError("client needs an operation")
    op, args = args[0], args[1:]
    flags = parse_flags(args, "version", "url", "tls-dir", "fixture", "connect-timeout-ms", "timeout-ms", "payload")
    edition_of(flags)
    url = urlsplit(required(flags, "url"))
    if url.scheme != "https" or not url.hostname:
        raise UsageError(f"--url {flags['url']!r} is not an https:// URL")
    positive_int(flags, "connect-timeout-ms", 10000, 3600000)
    positive_int(flags, "timeout-ms", 30000, 3600000)
    if op not in OPERATIONS + ("subscribe", "run"):
        raise UsageError(f"unknown client operation {op!r}")
    required(flags, "tls-dir")
    raise CodedError(EXIT_UNSUPPORTED, "Envoy is a server only: there is no client role")


# ---- server ----


@dataclass
class Device:
    serial: str
    lfdi: str
    sfdi: int


@dataclass
class Processes:
    """The child processes, in start order; stopped in reverse."""

    running: list[tuple[str, subprocess.Popen[bytes]]] = field(default_factory=list)

    def start(self, name: str, argv: list[str], env: dict[str, str], cwd: Path | None = None) -> None:
        # stdout belongs to the event stream: children log to stderr.
        proc = subprocess.Popen(argv, env=env, cwd=cwd, stdin=subprocess.DEVNULL,
                                stdout=sys.stderr, stderr=sys.stderr, start_new_session=True)
        self.running.append((name, proc))

    def exited(self) -> str | None:
        for name, proc in self.running:
            if proc.poll() is not None:
                return f"{name} exited with {proc.returncode}"
        return None

    def stop(self) -> None:
        for name, proc in reversed(self.running):
            if proc.poll() is None:
                # PostgreSQL shuts down fast on SIGINT; the others on SIGTERM.
                proc.send_signal(signal.SIGINT if name == "postgres" else signal.SIGTERM)
                try:
                    proc.wait(timeout=8)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait()


def lfdi_sfdi(cert_pem: bytes) -> tuple[str, int]:
    """LFDI (40 upper-case hex digits) and SFDI of a certificate."""
    from cryptography import x509
    from cryptography.hazmat.primitives.serialization import Encoding

    der = x509.load_pem_x509_certificate(cert_pem).public_bytes(Encoding.DER)
    lfdi = hashlib.sha256(der).hexdigest()[:40].upper()
    raw = int(lfdi[:9], 16)
    return lfdi, raw * 10 + (10 - sum(int(d) for d in str(raw)) % 10) % 10


def load_devices(directory: Path) -> list[Device]:
    try:
        paths = sorted(directory.glob("*.crt"))
    except OSError as exc:
        raise CodedError(EXIT_FIXTURE, f"--tls-dir: {exc}") from None
    devices = []
    for path in paths:
        try:
            lfdi, sfdi = lfdi_sfdi(path.read_bytes())
        except (OSError, ValueError) as exc:
            raise CodedError(EXIT_FIXTURE, f"--tls-dir: {path}: {exc}") from None
        devices.append(Device(serial=path.stem, lfdi=lfdi, sfdi=sfdi))
    return devices


def wait_for(check: Any, what: str, processes: Processes, timeout: float = 60) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if (problem := processes.exited()) is not None:
            raise CodedError(EXIT_FAILED, f"waiting for {what}: {problem}")
        if check():
            return
        time.sleep(0.2)
    raise CodedError(EXIT_FAILED, f"{what} did not start within {timeout:g} s")


def listening(port: int) -> bool:
    with socket.socket() as s:
        s.settimeout(0.5)
        return s.connect_ex(("127.0.0.1", port)) == 0


def healthy(port: int) -> bool:
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/status/health?check_data=false", timeout=2) as resp:
            return resp.status == 200
    except (OSError, urllib.error.URLError):
        return False


class Admin:
    """Envoy's admin API, with its basic authentication."""

    def __init__(self, username: str, password: str) -> None:
        self.auth = "Basic " + base64.b64encode(f"{username}:{password}".encode()).decode()

    def call(self, method: str, path: str, body: Any = None) -> tuple[dict[str, str], Any]:
        data = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(f"http://127.0.0.1:{ADMIN_PORT}{path}", data=data, method=method,
                                         headers={"Authorization": self.auth, "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(request, timeout=30) as resp:
                text = resp.read().decode()
                return dict(resp.headers), json.loads(text) if text else None
        except urllib.error.HTTPError as exc:
            raise CodedError(EXIT_FAILED, f"admin {method} {path}: {exc.code} {exc.read().decode()[:300]}") from None


async def seed_sites(database_url: str, devices: list[Device], fixture: dict[str, Any]) -> list[int]:
    """Inserts Envoy's NULL aggregator and a site per device, through Envoy's
    models and CRUD layer, as Envoy's demo database initialisation does."""
    from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine

    from envoy.server.crud.site import upsert_site_for_aggregator
    from envoy.server.model.aggregator import NULL_AGGREGATOR_ID, Aggregator
    from envoy.server.model.site import Site

    engine = create_async_engine(database_url)
    now = datetime.now(timezone.utc)
    site_ids = []
    try:
        async with async_sessionmaker(engine, class_=AsyncSession, expire_on_commit=False)() as session:
            session.add(Aggregator(aggregator_id=NULL_AGGREGATOR_ID, name="NULL AGGREGATOR",
                                   created_time=now, changed_time=now))
            await session.flush()
            for device in devices:
                site = Site(
                    nmi=None,
                    aggregator_id=NULL_AGGREGATOR_ID,
                    timezone_id="UTC",
                    created_time=now,
                    changed_time=now,
                    lfdi=device.lfdi,
                    sfdi=device.sfdi,
                    device_category=int(fixture["device"]["deviceCategory"], 16),
                    registration_pin=fixture["device"]["pin"] // 10,
                )
                site_ids.append(await upsert_site_for_aggregator(session, NULL_AGGREGATOR_ID, site))
            await session.commit()
    finally:
        await engine.dispose()
    return site_ids


def seed_programs(admin: Admin, fixture: dict[str, Any], site_ids: list[int]) -> None:
    """A site group of the provisioned sites; per DER program a site control
    group; per control a site control for that site group."""
    admin.call("POST", "/site_group", {"name": SITE_GROUP, "default_group": False})
    _, group = admin.call("GET", f"/site_group/{SITE_GROUP}")
    for site_id in site_ids:
        admin.call("POST", f"/site_group/{SITE_GROUP}/assignments", {"site_id": site_id})
    now = datetime.now(timezone.utc).replace(microsecond=0)
    for program in fixture["derPrograms"]:
        headers, _ = admin.call("POST", "/site_control_group", {
            "description": program["description"], "primacy": program["primacy"], "fsa_id": 1,
        })
        location = {k.lower(): v for k, v in headers.items()}.get("location", "")
        control_group_id = location.rstrip("/").rsplit("/", 1)[-1]
        if not control_group_id.isdigit():
            raise CodedError(EXIT_FAILED, f"admin: no site control group id in Location {location!r}")
        controls = []
        for control in program["controls"]:
            request: dict[str, Any] = {
                "site_group_id": group["site_group_id"],
                "calculation_log_id": None,
                "duration_seconds": control["duration"],
                "start_time": (now + timedelta(seconds=control.get("start", 0))).isoformat(),
            }
            for name, value in control["base"].items():
                request[CONTROL_FIELDS[name]] = value
            controls.append(request)
        if controls:
            admin.call("POST", f"/site_control_group/{control_group_id}/controls", controls)


def haproxy_config(port: int, tls_dir: Path, pem: Path) -> str:
    return f"""global
    log stderr format raw local0 notice
    maxconn 256

defaults
    mode http
    log global
    option httplog
    timeout connect 5s
    timeout client 60s
    timeout server 60s

frontend ieee20305
    bind :{port} ssl crt {pem} ca-file {tls_dir / "ca.crt"} verify required ssl-min-ver TLSv1.2 ssl-max-ver TLSv1.2 ciphers {CIPHERS_OPENSSL}
    http-request del-header x-forwarded-client-cert
    http-request set-header x-forwarded-client-cert %[ssl_c_der,sha2(256),hex]
    default_backend envoy

backend envoy
    server envoy 127.0.0.1:{SERVER_PORT}
"""


def run_server(args: list[str]) -> int:
    flags = parse_flags(args, "version", "port", "tls-dir", "fixture", "ready-file", "control-socket")
    edition = edition_of(flags)
    tls_dir = Path(required(flags, "tls-dir"))
    port = positive_int(flags, "port", 443, 65535)
    ready_file = Path(flags.get("ready-file", DEFAULT_READY))
    fixture = load_fixture(flags.get("fixture", str(DEFAULT_FIXTURE)))
    if edition not in SERVED_EDITIONS:
        raise CodedError(EXIT_UNSUPPORTED, f"Envoy serves IEEE 2030.5 {', '.join(SERVED_EDITIONS)}, not {edition}")
    for name in ("server.crt", "server.key", "ca.crt"):
        if not (tls_dir / name).is_file():
            raise CodedError(EXIT_FIXTURE, f"--tls-dir {tls_dir} has no {name}")
    check_fixture(fixture)
    devices = load_devices(tls_dir / "devices")

    WORK.mkdir(parents=True, exist_ok=True)
    pem = WORK / "server.pem"
    pem.write_bytes((tls_dir / "server.crt").read_bytes() + b"\n" + (tls_dir / "server.key").read_bytes())
    pem.chmod(0o600)
    database_url = f"postgresql+asyncpg://interop@127.0.0.1:{PG_PORT}/envoy"
    admin_password = secrets.token_hex(16)
    env = {
        **os.environ,
        "HOME": str(WORK),
        "DATABASE_URL": database_url,
        "CERT_HEADER": "x-forwarded-client-cert",
        "ALLOW_DEVICE_REGISTRATION": "true",
        "ENABLE_NOTIFICATIONS": "false",
        "DEFAULT_TIMEZONE": "UTC",
        "ADMIN_USERNAME": "interop",
        "ADMIN_PASSWORD": admin_password,
    }
    os.environ.update({k: env[k] for k in ("DATABASE_URL", "ADMIN_USERNAME", "ADMIN_PASSWORD")})

    processes = Processes()
    stopping = False

    def on_signal(signum: int, _: Any) -> None:
        nonlocal stopping
        stopping = True

    signal.signal(signal.SIGTERM, on_signal)
    signal.signal(signal.SIGINT, on_signal)
    try:
        # PostgreSQL, on loopback, trusting local connections only.
        pgdata = WORK / "pgdata"
        subprocess.run(["initdb", "-D", str(pgdata), "-U", "interop", "--auth=trust", "-E", "UTF8",
                        "--no-instructions"], env=env, stdout=sys.stderr, check=True)
        processes.start("postgres", ["postgres", "-D", str(pgdata), "-c", "listen_addresses=127.0.0.1",
                                     "-c", f"port={PG_PORT}", "-c", f"unix_socket_directories={WORK}",
                                     "-c", "fsync=off"], env)
        wait_for(lambda: subprocess.run(["pg_isready", "-q", "-h", "127.0.0.1", "-p", str(PG_PORT)]).returncode == 0,
                 "PostgreSQL", processes)
        subprocess.run(["createdb", "-h", "127.0.0.1", "-p", str(PG_PORT), "-U", "interop", "envoy"],
                       env=env, stdout=sys.stderr, check=True)
        # Envoy's schema, by its own migrations; then the sites.
        subprocess.run([str(VENV / "alembic"), "upgrade", "head"], cwd=ALEMBIC_DIR, env=env,
                       stdout=sys.stderr, check=True)
        site_ids = asyncio.run(seed_sites(database_url, devices, fixture))

        processes.start("envoy", [str(VENV / "uvicorn"), "envoy.server.main:app", "--host", "127.0.0.1",
                                  "--port", str(SERVER_PORT), "--no-access-log"], env)
        processes.start("envoy-admin", [str(VENV / "uvicorn"), "envoy.admin.main:app", "--host", "127.0.0.1",
                                        "--port", str(ADMIN_PORT), "--no-access-log"], env)
        wait_for(lambda: healthy(SERVER_PORT) and healthy(ADMIN_PORT), "Envoy", processes)
        seed_programs(Admin("interop", admin_password), fixture, site_ids)

        config = WORK / "haproxy.cfg"
        config.write_text(haproxy_config(port, tls_dir, pem))
        processes.start("haproxy", ["haproxy", "-db", "-f", str(config)], env)
        wait_for(lambda: listening(port), "HAProxy", processes)
    except subprocess.CalledProcessError as exc:
        processes.stop()
        raise CodedError(EXIT_FAILED, f"{exc.cmd[0]} exited with {exc.returncode}") from None
    except BaseException:
        processes.stop()
        raise

    for device in devices:
        event("device-provisioned", sfdi=device.sfdi, lfdi=device.lfdi)
    ready_file.parent.mkdir(parents=True, exist_ok=True)
    ready_file.write_text("ready\n")
    event("ready", adapter=ADAPTER, version=edition, port=port, fixture=fixture["name"])

    code = EXIT_OK
    while not stopping:
        if (problem := processes.exited()) is not None:
            log(problem)
            code = EXIT_FAILED
            break
        time.sleep(0.2)
    processes.stop()
    try:
        ready_file.unlink()
    except OSError:
        pass
    event("stopped")
    return code


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

# Third-party notices: ieee20305-interop-epri

This image contains the ieee20305-interop adapter (MIT, see
`/licenses/ieee20305-interop/LICENSE`) and the following third-party
software, each under its own licence.

## EPRI IEEE 2030.5 Client

- Upstream: https://github.com/epri-dev/IEEE-2030.5-Client (Electric Power
  Research Institute)
- Version: v0.2.11, commit 505ee47d9c3ed43ce63087198d09f77338da627d
- Licence: BSD-3-Clause:
  `/licenses/github.com/epri-dev/IEEE-2030.5-Client/LICENSE`
- `/opt/epri/client_test` is built from that source with its own build
  script. Modifications: its build script accepts OpenSSL 3, and
  `adapters/epri/patches/0001-sha256-padding.patch` fixes the SHA-256
  padding. `/opt/epri/settings` are the device settings of that source.
  The client's compiled-in schema comes from the `sep.xsd` that EPRI
  distributes with it.

## OpenSSL, Python and Debian

The base image `debian:trixie-slim` (pinned by digest) and the Debian
packages `libssl3t64` (OpenSSL, Apache-2.0) and `python3` (PSF licence) at
the versions the Dockerfile pins, installed unmodified. Their copyright
files are in `/usr/share/doc/*/copyright`.

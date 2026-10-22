# Third-party notices: ieee20305-interop-envoy

This image contains the ieee20305-interop adapter (MIT, see
`/licenses/ieee20305-interop/LICENSE`) and the following third-party
software, each under its own licence.

## Envoy

- Upstream: https://github.com/bsgip/envoy (Battery Storage and Grid
  Integration Program, Australian National University)
- Version: v2.5.0, commit 9d00340b5592f1fdf09dc74bd743fc251da0bfb5
- Licence: MIT: `/licenses/github.com/bsgip/envoy/LICENSE.txt`
- Installed from that source, unmodified, in `/opt/venv`; its Alembic
  migrations in `/opt/envoy`.

## Python packages

Envoy's dependencies, installed from PyPI with the hashes of Envoy's own
`uv.lock`, and the build tools of `adapters/envoy/build-requirements.txt`.
Their names and versions are in `/licenses/BUNDLED-MODULES.txt` and their
licence files in `/licenses/<name>/`.

## HAProxy and PostgreSQL

The Debian trixie packages `haproxy` (GPL-2.0-or-later, with LGPL-2.1
headers) and `postgresql-17` (PostgreSQL licence), at the versions the
Dockerfile pins, installed unmodified from Debian. Their copyright files are
in `/usr/share/doc/haproxy/copyright` and
`/usr/share/doc/postgresql-17/copyright`; the source is available from
Debian (https://sources.debian.org/).

## Python and Debian

The base image `python:3.13-slim-trixie` (pinned by digest) carries CPython
(PSF licence) and Debian packages under their own licences, listed in
`/usr/share/doc/*/copyright`.

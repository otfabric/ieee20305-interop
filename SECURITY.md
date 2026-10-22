# Security Policy

## Reporting a Vulnerability

If you find a security vulnerability in `ieee20305-interop`, please report it
responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, email **security@otfabric.io**, or open a private advisory on
GitHub: on the [Security](https://github.com/otfabric/ieee20305-interop/security)
tab, choose **Advisories**, then **Report a vulnerability**. Include a
description, the steps to reproduce, and the affected image tag or digest.

Vulnerabilities in the bundled implementations themselves (EPRI Client, Envoy,
py20305, GridAPPS-D) belong upstream. Report them there, and tell us so that we
can move the pin.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| Older releases | No; pull the latest images |

## Security Considerations

### These are test fixtures, not products

The images run IEEE 2030.5 servers and clients with **generated test
certificates** for mutual TLS. The device identities (LFDI/SFDI) are
synthetic test values.

- **Never connect a reference client to a production utility server, and
  never connect a production DER device to a reference server.**
- Publish ports on `127.0.0.1` only. The self-tests use private Docker
  networks and publish nothing.
- The test certificates and device identities in the fixtures are public by
  design.

### TLS material

No private key is committed or baked into an image. mTLS scenarios generate
device certificates, SERCA (Smart Energy Root CA) hierarchies and LFDI values
when a test runs, and mount them read-only.

### Image hardening

- Processes run as a non-root user (uid 10001).
- Base images are pinned by digest, and upstreams by exact version and
  commit ([versions.yaml](versions.yaml)).
- Runtime images contain the adapter, its runtime and the fixtures. Build
  tools stay in the build stages.

# Contributing

## Ground rules

1. **Nothing here may know a library under test** (REQUIREMENTS.md R1). CI
   fails on references to one in adapters, fixtures, schemas, scripts or
   tests.
2. **The contract is the product.** Change
   [docs/CONTAINER_CONTRACT.md](docs/CONTAINER_CONTRACT.md) first, then every
   adapter. After 1.0, an addition bumps the minor version and a breaking
   change bumps the major version.
3. **Declare only what is tested.** A capability or listed operation needs a
   self-test that exercises it.
4. **Pin everything, and mirror it in versions.yaml.** `make versions`
   enforces this.
5. **Each adapter uses its own upstream engine.** Glue is fine. A shared
   IEEE 2030.5 implementation is not.

## Setup

- Docker with buildx
- Python 3.12 or later, with `jsonschema` (otherwise the checks run in a
  container)
- jq; shellcheck (otherwise it runs in a container)
- openssl (for generating test mTLS certificates)

```bash
make ci
```

## Adding a fixture

1. Create `fixtures/baseline/fixture.json` (or another named fixture).
2. Run `make fixtures`.
3. Document the fixture in [docs/FIXTURES.md](docs/FIXTURES.md).

Payloads must be valid for the IEEE 2030.5 version. XML resources should
conform to the IEEE 2030.5 XSD schema.

## Adding an adapter

1. Create `adapters/<name>/Dockerfile` (multi-stage, pinned bases, non-root
   uid 10001, health check, OCI labels, licences under `/licenses`).
2. Add the adapter to `ADAPTERS` in the Makefile and to `versions.yaml`.
3. The entrypoint is `ieee20305-interop`, implementing the commands of
   [docs/CONTAINER_CONTRACT.md](docs/CONTAINER_CONTRACT.md).
4. Run `make image-<name> smoke-<name>`.

## TLS material

No private key is committed or baked into an image. mTLS test scenarios
generate device certificates (with LFDI/SFDI) and CA certificates when a
test runs, and mount them read-only. See `scripts/lib.sh` for the helpers.

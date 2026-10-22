# ieee20305-interop Releases

## How a release is made

1. Update the "## Unreleased" notes below as `## vX.Y.Z` with today's date
   and set `interop_version` in versions.yaml. Commit it through a pull
   request.
2. Run `make ci` on the merged `main` to verify.
3. Tag that commit `vX.Y.Z` and push the tag. The `Release` workflow
   validates the repository, runs the same checks, and creates the GitHub
   release.

The release also builds and self-tests the images natively on amd64 and
arm64, with the same jobs as CI. It then pushes exactly the tested images as
per-architecture tags, joins them into a multi-arch index on GHCR, and lists
the index digests in the release. Consumers pin the digests. Every adapter
with a Dockerfile must be in the Makefile's `ADAPTERS`, or the release fails.

## Unreleased

### Summary

Initial scaffolding. All adapters are in status `planned`: Dockerfiles are
skeleton placeholders. No images are published yet.

### Added

- Repository layout, licence, Makefile, CI workflow
- Container contract draft (v0.1)
- Fixture schema and baseline fixture
- Capabilities schema
- Shell scripts: lib.sh, smoke.sh, interop.sh, retry.sh, check-versions.sh
- Skeleton Dockerfiles for epri, envoy, py20305 and gridappsd
- versions.yaml with the four candidate upstreams

# evidence

`maya-stall evidence` collects and publishes Evidence Bundles.

## collect

`maya-stall evidence collect` runs a Scenario and writes a complete Evidence
Bundle.

```sh
maya-stall evidence collect smoke
maya-stall evidence collect --host-config ci-hosts.yaml --target-profile ci smoke
maya-stall evidence collect --host-config ci-hosts.yaml --target-profile ci --host maya-win-01 smoke
```

The bundle includes:

- `evidence.json`
- `manifest.json`
- Scenario Result JSON
- events and logs
- Visual Evidence artifacts
- declared output files

Validator failures are recorded in `evidence.json` and mark the run failed.

The fake broker supports configured Visual Evidence. With
`broker.type: gg-mayasessiond`, screenshot and recording Visual Evidence use an
interactive Windows scheduled task to capture the visible desktop session that
owns Maya. Recording uses 10 seconds at 15 fps by default and is encoded locally
with `ffmpeg`.

## publish

`maya-stall evidence publish` copies one Evidence Bundle to a filesystem
Evidence Store and writes the published manifest plus Review Comment markdown.

```sh
maya-stall evidence publish \
  --destination /mnt/evidence/maya-stall \
  --base-url https://evidence.example.com/maya-stall \
  artifacts/maya-stall/<run-id>
```

Publishing writes:

```text
<destination>/<run-id>/artifact-manifest.json
<destination>/<run-id>/review-comment.md
```

Publishing the same run again replaces the previous published run directory so
stale files do not survive.

## live proof artifact

The protected GitHub Actions live proof workflow can also publish a sanitized
downloadable artifact named `live-visual-evidence-proof`. It is disabled by
default and enabled only in the live workflow through
`MAYA_STALL_LIVE_PROOF_ARTIFACT_ENABLED=true`.

The artifact contains only reviewer-facing proof:

- `proof-artifact-manifest.json`
- `evidence-metadata.json`
- `media-review.json`
- `screenshots/desktop-screenshot.png`
- `recordings/smoke.mp4`

The MP4 comes from a recording-enabled Scenario Evidence Bundle captured from
the interactive desktop session. The live proof smoke also validates the
standalone `maya-stall record` path before publishing sanitized review proof.

Retention is short and configurable with
`MAYA_STALL_LIVE_PROOF_RETENTION_DAYS` or the matching workflow variable.
Private host names should be replaced with
`MAYA_STALL_LIVE_PROOF_PUBLIC_HOST_ALIAS` before upload.

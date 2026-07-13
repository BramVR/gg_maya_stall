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

Every Visual Evidence artifact carries Visual Evidence Provenance in
`evidence.json`: an `origin` value plus a `sha256` content hash computed at
capture or collection time. Origin values are:

- `broker-capture`: captured through a real Session Broker.
- `fake-broker-capture`: captured by the fake Session Broker.
- `discovered`: a file found under `screenshots/` or `recordings/` that was not
  registered by a Session Broker capture.

Session Broker captures also append `broker.screenshot.capture-requested`,
`broker.screenshot.captured`, `broker.recording.capture-requested`, and
`broker.recording.captured` provenance events (with origin and sha256) to
`events.jsonl`. Live-proof-eligible bundles fail on `discovered` Visual
Evidence; `fake-broker-capture` artifacts are only accepted in the fake
runtime.

The fake broker supports configured Visual Evidence. With
`broker.type: gg-mayasessiond`, screenshot and recording Visual Evidence use an
interactive Windows scheduled task to capture the visible desktop session that
owns Maya. On real Windows Maya Hosts, both screenshots and recording frames
cover the full Windows virtual desktop across attached monitors rather than
only the primary screen. Recording uses 10 seconds at 15 fps by default and is
encoded locally with `ffmpeg`.

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

`artifact-manifest.json` carries each Visual Evidence artifact's `origin` and
`sha256` through from the Evidence Bundle, so published manifests match bundle
manifests.

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
- `recordings/recording.mp4`

The MP4 comes from the standalone `maya-stall record` Evidence Bundle. The live
proof smoke also validates a recording-enabled Scenario through the paired live
run gate before accepting the PR.

The live proof artifact only accepts bundles whose Visual Evidence carries
`broker-capture` provenance with a `sha256` that matches the artifact bytes;
`discovered` or `fake-broker-capture` origins fail the publish.

Retention is short and configurable with
`MAYA_STALL_LIVE_PROOF_RETENTION_DAYS` or the matching workflow variable.
Private host names should be replaced with
`MAYA_STALL_LIVE_PROOF_PUBLIC_HOST_ALIAS` before upload.

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
`broker.type: gg-mayasessiond`, screenshot Visual Evidence uses
`viewport.capture`. Recording evidence is deferred for v1 until the Session
Broker exposes real recording capture.

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

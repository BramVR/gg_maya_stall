# Maya Stall

Maya Stall is a Go CLI for real Autodesk Maya UI end-to-end checks from consuming repos.

## Check

```sh
go test ./...
go build ./cmd/maya-stall
```

## Start a consuming repo config

```sh
maya-stall init
```

`maya-stall init` writes `.maya-stall.yaml` with a repo-only sample smoke Scenario. Keep Host Credentials, Host Pools, SSH keys, hostnames, and private infrastructure details outside repo config.

## Run a fake Scenario

```sh
maya-stall run smoke
```

`maya-stall run <scenario>` selects a named Scenario from repo config, stages only its declared Run Payload paths into hidden run state, and writes a minimal local Evidence Bundle under `artifacts/maya-stall/`.
Run Payload entries are typed: `pluginArtifacts`, `mayaScripts`, `scenes`, repo-owned `expectedOutputs`, and `includePaths`. Maya Stall stages declared paths even when a consuming repo ignores them, such as build outputs under `build/`.

Scenarios can add generic Validators:

```yaml
validators:
  - type: scenarioResultStatus
    status: passed
  - type: outputExists
    path: outputs/report.json
  - type: jsonEquals
    path: outputs/report.json
    jsonPath: $.plugin.loaded
    equals: true
  - type: numericApprox
    path: outputs/report.json
    jsonPath: $.timings.solveMs
    equals: 12.5
    tolerance: 0.25
  - type: fileHash
    path: outputs/report.json
    sha256: "<sha256>"
  - type: visualEvidence
    required: true
```

Validator failures mark the run failed and are recorded in `evidence.json`.

## Collect Visual Evidence

```sh
maya-stall screenshot
maya-stall record
maya-stall evidence collect smoke
maya-stall evidence publish --destination /mnt/evidence/maya-stall --base-url https://evidence.example.com/maya-stall artifacts/maya-stall/<run-id>
```

`maya-stall screenshot` and `maya-stall record` ask the fake Session Broker to capture a standalone screenshot or recording, then store a local Evidence Bundle under `artifacts/maya-stall/`. `maya-stall evidence collect <scenario>` runs the Scenario, captures configured Visual Evidence through the fake Session Broker, writes `evidence.json`, `manifest.json`, events, logs, Scenario Result, and visual artifacts, and prints validator failures such as missing required Visual Evidence.

`maya-stall evidence publish` copies one Evidence Bundle to a filesystem Evidence Store under `<destination>/<run-id>/`, generates artifact URLs from `--base-url`, and writes `artifact-manifest.json` plus `review-comment.md`. The Review Comment markdown summarizes run status and links Visual Evidence, logs, metadata, and output files from the bundle. Publishing the same run again replaces the previous published run directory so stale files do not survive.

## Write a Scenario Result

Maya Stall passes the Scenario Result path to the Maya Script environment as `MAYA_STALL_SCENARIO_RESULT`. Scripts can use the optional Python helper:

```python
import maya_stall

maya_stall.write_result(
    status="passed",
    summary="Plugin loaded and smoke check completed.",
    assertions=[
        {"name": "plugin loaded", "passed": True},
    ],
    measurements={"solveMs": 12.5},
    outputs={"report": "outputs/report.json"},
)
```

The helper writes JSON to `MAYA_STALL_SCENARIO_RESULT`, creating parent directories as needed. It is intentionally small; Scenario authors can also emit the protocol directly:

```python
import json
import os

result = {
    "status": "passed",
    "summary": "Plugin loaded and smoke check completed.",
    "assertions": [{"name": "plugin loaded", "passed": True}],
}

path = os.environ["MAYA_STALL_SCENARIO_RESULT"]
os.makedirs(os.path.dirname(path), exist_ok=True)
with open(path, "w", encoding="utf-8") as handle:
    json.dump(result, handle, indent=2)
    handle.write("\n")
```

Host Pools live outside repo config. A user or CI host config can map Target Profiles to Host Pools:

```yaml
version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        health: healthy
      - id: beta
        health: healthy
```

```sh
maya-stall run --host-config ci-hosts.yaml --target-profile ci smoke
maya-stall run --host-config ci-hosts.yaml --target-profile ci --host beta smoke
maya-stall run --host-config ci-hosts.yaml --target-profile ci --host-lock-wait 30s smoke
```

The fake runtime chooses the first healthy unlocked Maya Host, writes the selected Target Profile and Maya Host into run output and manifests, and holds a Host Lock under `.maya-stall/state/locks/hosts/` for the Fresh Run.

Fresh Runs stop and clean hidden run state by default after writing the Evidence Bundle. Use `--keep-on-failure` to leave a failed Kept Session for local debugging, or set the Stop Policy explicitly:

```sh
maya-stall run --keep-on-failure smoke
maya-stall run --stop-after success smoke
maya-stall run --stop-after failure smoke
maya-stall run --stop-after always smoke
maya-stall run --stop-after never smoke
```

Kept Sessions stay visible in fake local run state and keep their Host Lock until stopped:

```sh
maya-stall status
maya-stall status --run <run-id>
maya-stall attach <run-id>
maya-stall stop <run-id>
```

`attach` prints the kept run's events and Session Broker log. `stop` removes the kept run state and releases the Host Lock.

## Check fake Host Health

```sh
maya-stall doctor
maya-stall doctor --host-config ci-hosts.yaml --target-profile ci --host beta
maya-stall doctor --host-config ci-hosts.yaml --target-profile ci --scenario smoke
```

`maya-stall doctor` reports layered local, Target Profile, Host Pool, fake SSH, work root, Session Broker, Maya version, Visual Evidence, Host Lock, and Scenario input checks. Failures name the failed layer and include a repair hint. Default checks stay fake/local; no real Maya, SSH, hostnames, credentials, or Evidence Store are required.

Host config may include fake diagnostic fields for deterministic checks:

```yaml
version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: beta
        ssh: ok
        workRoot: writable
        broker: ok
        mayaVersions: ["2025"]
        visualEvidence: true
```

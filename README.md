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
maya-stall review-comment github --repo owner/repo --pr 123 /mnt/evidence/maya-stall/<run-id>
maya-stall review-comment gitlab --project group/project --merge-request 123 /mnt/evidence/maya-stall/<run-id>
```

`maya-stall screenshot` and `maya-stall record` ask the fake Session Broker to capture a standalone screenshot or recording, then store a local Evidence Bundle under `artifacts/maya-stall/`. `maya-stall evidence collect <scenario>` runs the Scenario, captures configured Visual Evidence through the fake Session Broker, writes `evidence.json`, `manifest.json`, events, logs, Scenario Result, and visual artifacts, and prints validator failures such as missing required Visual Evidence.

Visual Evidence recording defaults follow the selected Crabbox timing slice: normal recordings use 10 seconds at 15 fps. Other Crabbox-like timing defaults stay in the ADRs until Maya Stall needs that behavior. See `docs/adr/0024-vendor-useful-crabbox-code.md` for source attribution.

`maya-stall evidence publish` copies one Evidence Bundle to a filesystem Evidence Store under `<destination>/<run-id>/`, generates artifact URLs from `--base-url`, and writes `artifact-manifest.json` plus `review-comment.md`. The Review Comment markdown summarizes run status and links Visual Evidence, logs, metadata, and output files from the bundle. Publishing the same run again replaces the previous published run directory so stale files do not survive.

`maya-stall review-comment` renders Review Comment markdown from the published `artifact-manifest.json`, rewrites `review-comment.md`, then creates or updates one marked platform comment. GitHub uses `GITHUB_TOKEN` by default and needs `--repo <owner/name>` plus `--pr <number>`. GitLab uses `GITLAB_TOKEN` by default and needs `--project <path-or-id>` plus `--merge-request <iid>`. Use `--token-env <name>` to read a different exact token variable, `--api-url` for GitHub Enterprise, `--base-url` for self-managed GitLab, or `--dry-run` to render locally without credentials or network access.

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

For real host preparation, use the [Windows Maya Host setup checklist](docs/setup/windows-maya-host.md). Maya Stall diagnoses host prerequisites; OpenSSH, interactive desktop, Autodesk Maya, `gg_mayasessiond`, work roots, Visual Evidence capture, and Evidence Store access stay host-managed.

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

## Opt in to real SSH transport

Default tests and default commands still use fake/local transport. Real SSH is enabled only by user or CI host config outside the consuming repo:

```yaml
version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: maya-win-01
        transport: ssh
        ssh:
          host: maya-win-01
          user: maya-runner
          port: 22
          identityFile: ~/.ssh/maya-stall-ci
          sftpTimeout: 30m
        workRoot: C:/maya-stall
        broker: ok
        mayaVersions: ["2025"]
        visualEvidence: true
```

With `transport: ssh`, `maya-stall doctor` runs real SSH connectivity and writable work-root checks. `maya-stall run` uploads declared Run Payload paths with `sftp` into a clean remote run workspace under `workRoot/runs/<run-id>/`, then downloads declared `expectedOutputs.scenarioResult` and `expectedOutputs.files` back into the local Evidence Bundle path. `ssh.sftpTimeout` defaults to `30m`; set it to `0` to rely on SSH keepalives without a wall-clock transfer cap. Session Broker launch remains a separate layer.

Opt-in live smoke is skipped unless the exact host config env var is set:

```sh
MAYA_STALL_SMOKE_HOST_CONFIG=/path/to/ci-hosts.yaml go test ./internal/cli -run TestOptInRealSSHDoctorSmoke -count=1
```

Optional smoke env vars:

- `MAYA_STALL_SMOKE_TARGET_PROFILE`: Target Profile; default `default`.
- `MAYA_STALL_SMOKE_HOST`: pinned Maya Host id.

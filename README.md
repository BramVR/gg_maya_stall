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

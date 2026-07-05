# Getting Started

Read this when:

- you are new to Maya Stall and want a fake Scenario running quickly;
- you are evaluating whether a repo can become a consuming repo;
- you need the normal path from repo config to Evidence Bundle.

This is a cookbook, not the full reference. For flags and command behavior, see
the [CLI overview](cli.md) and [Command reference](commands/README.md).

## Step 1. Build And Verify

From this repository:

```sh
go test ./...
go build -o bin/maya-stall ./cmd/maya-stall
```

Verify the binary:

```sh
./bin/maya-stall version
./bin/maya-stall --help
```

`doctor` needs repo config, so run it after `maya-stall init` in the next steps.
With no real host config, the default path stays fake/local and should not
require Autodesk Maya, private hosts, or secrets.

## Step 2. Add Repo Config

In a consuming repo:

```sh
maya-stall init
```

`init` writes `.maya-stall.yaml` with a safe repo-only sample Scenario. Keep
private hostnames, Host Credentials, SSH keys, Windows users, license details,
Host Pools, and Evidence Store paths outside repo config.

The config shape is Scenario-first:

```yaml
version: 1
scenarios:
  smoke:
    payload:
      mayaScripts:
        - tests/maya/smoke.py
      pluginArtifacts:
        - build/plugin.mll
      expectedOutputs:
        - golden/expected.json
    expectedOutputs:
      scenarioResult: outputs/result.json
      files:
        - outputs/report.json
    evidence:
      screenshots:
        enabled: true
      recording:
        enabled: false
    validators:
      - type: scenarioResultStatus
        status: passed
      - type: visualEvidence
        required: true
```

## Step 3. Run The Fake Smoke

```sh
maya-stall doctor --scenario smoke
maya-stall run smoke
```

The fake runtime selects the Scenario, stages the declared Run Payload paths,
writes local run state, produces an Evidence Bundle under `artifacts/maya-stall/`,
and exits with the Scenario result.

Use a Host Lock wait when multiple runs may target the same Maya Host:

```sh
maya-stall run --host-lock-wait 30s smoke
```

Keep failed sessions around for local debugging:

```sh
maya-stall run --keep-on-failure smoke
maya-stall status
maya-stall attach <run-id>
maya-stall stop <run-id>
```

## Step 4. Write A Scenario Result

Maya Stall passes the Scenario Result path to Maya scripts as
`MAYA_STALL_SCENARIO_RESULT`. Scripts can use the optional Python helper:

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

The helper writes JSON and creates parent directories as needed. Scenario
authors may also write the protocol directly.

## Step 5. Add Validators

Validators compare Evidence Bundle outputs against Expected Outputs:

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

## Step 6. Collect And Publish Evidence

Collect a full Evidence Bundle:

```sh
maya-stall evidence collect smoke
```

Capture standalone visual evidence when debugging:

```sh
maya-stall screenshot
maya-stall record
```

Publish the bundle to a filesystem Evidence Store:

```sh
maya-stall evidence publish \
  --destination /mnt/evidence/maya-stall \
  --base-url https://evidence.example.com/maya-stall \
  artifacts/maya-stall/<run-id>
```

Then publish a platform Review Comment:

```sh
maya-stall review-comment github \
  --repo owner/repo \
  --pr 123 \
  /mnt/evidence/maya-stall/<run-id>

maya-stall review-comment gitlab \
  --project group/project \
  --merge-request 123 \
  /mnt/evidence/maya-stall/<run-id>
```

Use `--dry-run` to render locally without credentials or network writes.

## Step 7. Prepare A Real Windows Maya Host

Follow the [Windows Maya Host setup checklist](setup/windows-maya-host.md). The
host must provide OpenSSH, an interactive logged-in desktop, Autodesk Maya, a
Session Broker, writable work roots, and Visual Evidence support.

Real SSH transport is enabled only in host config outside the consuming repo:

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
        workRoot: C:/maya-stall
        broker:
          type: gg-mayasessiond
          stateDir: C:/maya-stall/sessiond-ui
          python: C:/maya-stall/sessiond-venv311/Scripts/python.exe
          repo: C:/maya-stall/tools/gg_mayasessiond
          mcpSource: C:/maya-stall/tools/GG_MayaMCP
        mayaVersions: ["2025"]
        visualEvidence: true
```

Run doctor against the exact profile or host before a long workflow:

```sh
maya-stall doctor --host-config ci-hosts.yaml --target-profile ci
maya-stall doctor --host-config ci-hosts.yaml --target-profile ci --host maya-win-01 --scenario smoke
```

Start `gg_mayasessiond` with script execution allowed for staged run wrappers,
for example `--mcp-script-dirs C:/maya-stall/runs`. The full live smoke is
opt-in:

```sh
MAYA_STALL_SMOKE_HOST_CONFIG=/path/to/ci-hosts.yaml go test ./internal/cli -run 'TestOptInRealSSH(Doctor|Run)Smoke' -count=1
```

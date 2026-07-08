# Windows Maya Host Setup

Read when preparing a Maya Host, diagnosing `maya-stall doctor` failures, or checking that a Target Profile can run a real Maya UI Session.

This guide is a host-admin checklist. Maya Stall diagnoses these prerequisites but does not install or configure them. The CLI should report the failing Host Health layer and point back here; the Windows machine, license, accounts, SSH identity, Session Broker, work roots, and Evidence Store access remain host-managed.

Maya Stall uses Crabbox as a reference for static SSH, leases, desktop evidence, and artifact publishing, but it is not Crabbox managed cloud bootstrap. Crabbox-managed Windows leases may create and auto-logon a desktop user. Maya Stall v1 targets owned Windows Maya Hosts, so setup happens before `maya-stall run`.

## Checklist

### Prepare Script

Maya Stall includes a host-admin prepare script for already-owned,
already-licensed Windows Maya Hosts:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/windows/prepare-maya-host.ps1 `
  -CheckOnly `
  -MayaExe "C:\Program Files\Autodesk\Maya2025\bin\maya.exe" `
  -SessiondRepo "C:\maya-stall-src\GG_MayaSessiond" `
  -McpSource "C:\maya-stall-src\GG_MayaMCP"
```

Run it on the Windows Maya Host after OpenSSH, the Windows account,
interactive desktop/login, Autodesk Maya licensing, and the Session Broker
source checkouts are already present. `-CheckOnly` reports planned changes and
readiness without mutating the host. Without `-CheckOnly`, the script creates
or verifies:

- `C:\maya-stall`
- `C:\maya-stall\runs`
- `C:\maya-stall\artifacts`
- `C:\maya-stall\sessiond-ui`
- `C:\maya-stall\sessiond-venv311`
- `C:\maya-stall\start-sessiond-ui.cmd`
- interactive scheduled task `MayaStallSessiondUI`

The generated launcher starts `gg_maya_sessiond.cli start` with the configured
Maya executable, `GG_MayaMCP` source, Session Broker state directory, Python
virtual environment, and `--mcp-script-dirs C:\maya-stall\runs` so staged
Scenario wrappers can run. Existing generated launchers are updated
idempotently. Existing unmarked launchers are not overwritten unless the host
admin passes `-Force`. Apply mode starts `MayaStallSessiondUI` after creating
or updating it so the printed doctor command can run immediately on a logged-in
interactive desktop. Pass `-NoStartTask` if an operator wants to update the host
shape without starting the Session Broker.

The script also prints a host-config YAML snippet and the matching
`maya-stall doctor --host-config ... --target-profile ... --host ... --scenario smoke`
command. Treat that output as an operator starting point: keep private
hostnames, SSH key paths, Windows users, license details, and Evidence Store
credentials in user or CI host config, not in `.maya-stall.yaml`.

Example apply:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/windows/prepare-maya-host.ps1 `
  -MayaExe "C:\Program Files\Autodesk\Maya2025\bin\maya.exe" `
  -SessiondRepo "C:\maya-stall-src\GG_MayaSessiond" `
  -McpSource "C:\maya-stall-src\GG_MayaMCP" `
  -HostId "maya-win-01" `
  -TargetProfile "ci" `
  -SshHost "maya-win-01" `
  -SshUser "maya-runner" `
  -SftpTimeout "30m"
```

Doctor layer:

- `work-root`: rerun the prepare script or repair permissions if the work root
  layout is missing or unwritable.
- `session-broker`: rerun the prepare script or repair the interactive desktop,
  generated launcher, scheduled task, `gg_mayasessiond`, `GG_MayaMCP`, Maya
  executable, or Python virtual environment.

### Target Profile And Host Pool

- Keep Target Profiles, Host Pools, Host Credentials, private hostnames, SSH keys, Windows users, license details, and Evidence Store locations outside Repo Run Config.
- Put only non-secret Scenario and Run Payload configuration in `.maya-stall.yaml`.
- Give each Maya Host a stable id in user or CI host config.
- Decide how the Target Profile selects from the Host Pool: first healthy unlocked host, pinned Maya Host, wait, or fail fast.

Doctor layer:

- `target-profile`: missing or invalid Target Profile in host config.
- `host-pool`: missing Host Pool, empty Host Pool, invalid Maya Host id, or no healthy Maya Host.
- `host`: pinned Maya Host not found or not selectable from the Target Profile.

### OpenSSH Reachability

- Enable OpenSSH Server on the Windows Maya Host.
- Configure a non-interactive SSH identity for the user or CI runner that owns the Maya Stall run.
- Ensure the controller machine has `ssh` and `scp`/`sftp` client tools
  available. Maya Stall uses SSH for commands and SFTP for payload/output
  transfer.
- Confirm SSH reaches the expected Windows account and does not expose passwords or private keys in repo config.
- Keep host aliases and private network addresses in operator config such as SSH config, CI secrets, or host config.
- Enable real SSH transport only in user or CI host config, not in `.maya-stall.yaml`:

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

Doctor layer:

- `fake-ssh`: fake/local SSH reachability status would fail for deterministic default tests.
- `ssh`: real SSH reachability failed for a `transport: ssh` Maya Host. Repair host networking, SSH service state, keys, or host config before retrying.
- `ssh.sftpTimeout` defaults to `30m`; set `0` only when job-level timeouts and SSH keepalives are the desired transfer bound.

### Work Root

- Create a writable work root on the Maya Host for Maya Stall run state.
- Reserve subdirectories for staged Run Payloads, clean per-run workspaces, Session Broker state, logs, and transient artifacts.
- Confirm the SSH user and Session Broker user can read and write the same work root.
- Plan retention for old run workspaces and Kept Sessions so Host Locks do not hide disk pressure.
- With `transport: ssh`, `maya-stall run` uploads declared Run Payload entries to `workRoot/runs/<run-id>/payload/` and downloads declared `expectedOutputs.scenarioResult` and `expectedOutputs.files` from `workRoot/runs/<run-id>/workspace/` back into the local Evidence Bundle.

Doctor layer:

- `work-root`: work root is missing, unwritable, or mapped to a path the Session Broker cannot use.

### Interactive Desktop

- Keep a real interactive Windows desktop available for Maya UI Sessions.
- Ensure Maya UI processes run in the interactive console session, not Windows Services session `0`.
- Avoid treating a raw SSH-launched `maya.exe` as proof of a usable Maya UI Session.
- If the host uses an interactive scheduled task or similar helper, make it part of Session Broker setup and keep the path configurable.

Wrong signal:

```text
Session Name: Services
Session#: 0
```

Expected signal:

```text
Session Name: Console
```

Doctor layer:

- `session-broker`: if the broker cannot prove or create an interactive Maya UI Session, repair the broker launch strategy and desktop login state.

### Autodesk Maya

- Install an Autodesk Maya version compatible with the Scenarios that will run on this Maya Host.
- Make the Maya executable path available to the Session Broker through host-managed config.
- Ensure licensing is valid for the Windows user that owns the interactive desktop.
- Verify Plugin Artifacts from consuming repos can load in that Maya version; Maya Stall does not build Plugin Artifacts.

Doctor layer:

- `maya-version`: selected Scenario has a Maya Version Requirement that the Maya Host does not satisfy.

### Session Broker

- Install and configure `gg_mayasessiond` as the v1 Session Broker for the Maya Host.
- Run `gg_mayasessiond` on the Windows Maya Host and reach it through SSH/Tailscale from Maya Stall. Do not configure Maya Stall to look for a Mac-local daemon.
- Run the Session Broker from the interactive desktop path, not as a headless service-only Maya launcher.
- Give it access to the work root, Maya executable, MCP or helper code it needs, and any configured state directory.
- Allow `script.execute` to run scripts staged under `workRoot/runs`, such as `--mcp-script-dirs C:/maya-stall/runs` for `gg_mayasessiond`.
- Keep host-specific paths in host-managed config; do not bake them into consuming repos.
- Treat Host and Session Broker as one runtime contract. Default local tests use `fake-local` (fake Maya Host plus fake Session Broker). Real SSH Maya Hosts use `ssh-sessiond` (SSH Windows Maya Host plus `gg_mayasessiond`). SSH Host plus fake broker, missing broker config, malformed `gg_mayasessiond` config, or fake Host plus real broker fail before payload staging or proof capture.
- Configure the broker as a structured host-config block:

```yaml
broker:
  type: gg-mayasessiond
  stateDir: C:/maya-stall/sessiond-ui
  python: C:/maya-stall/sessiond-venv311/Scripts/python.exe
  repo: C:/maya-stall-src/GG_MayaSessiond
  mcpSource: C:/maya-stall-src/GG_MayaMCP
```

Maya Stall invokes `gg_maya_sessiond.cli` on the Windows host through the same SSH transport. Runs stage declared payloads under `workRoot/runs/<run-id>/`, execute a staged wrapper with `script.execute`, download declared outputs from the remote workspace, and capture configured Visual Evidence from the interactive Windows desktop. Remote Scenario execution through `script.execute` is capped at 10 minutes. The Session Broker launcher must allow the staged wrapper directory; otherwise doctor fails the `session-broker` layer with a `script.execute` repair hint.

Each `manifest.json` and `evidence.json` records the resolved runtime profile, host adapter, broker adapter, redacted broker config source, and whether the run is eligible for live product proof.

`maya-stall doctor` also performs live broker probes for `gg_mayasessiond`: it runs daemon `doctor` and `status`, checks the Windows `maya.exe` session, stages a tiny probe script under `workRoot/runs/doctor-*`, executes it with `script.execute`, removes that probe directory, and checks `viewport.capture`. The local Host Lock gates these probes for Maya Stall runs from the same checkout, but operators should still treat doctor as a live diagnostic that briefly executes code in the active Maya session.

The Doctor CLI renders a Host Health report rather than independent text-only checks. Tests and live proof use the same report fields for layer status, Host Lock state, interactive desktop proof, and broker-backed Visual Evidence readiness.

Doctor layer:

- `session-broker`: broker unreachable, unhealthy, misconfigured, stale, missing `maya.exe`, or launching Maya in Windows Services session `0` instead of the interactive desktop.

### Visual Evidence

- Confirm the Session Broker can capture screenshots and recordings from the
  same desktop session as the Maya UI Session.
- Install local `ffmpeg` on the controller machine that runs `maya-stall`.
  Recording capture downloads Windows desktop frames and encodes the MP4
  locally.
- Keep Windows PowerShell available on the Maya Host with
  `System.Windows.Forms` and `System.Drawing` desktop assemblies.
- Keep `schtasks.exe` available and usable with `/IT` so capture work can run
  in the logged-in interactive desktop session instead of the raw SSH session.
- Keep `Compress-Archive` available on the Windows host so recording frames can
  be zipped for transfer.
- Desktop control uses the same short-lived `/IT` scheduled-task path and
  `user32.dll` desktop APIs for explicit coordinate clicks.
- Store screenshots, recordings, logs, Scenario Results, and declared outputs
  in each Evidence Bundle.
- When broker execution fails before Scenario Result collection, Maya Stall
  writes a failed Evidence Bundle and best-effort captures
  `screenshots/failure-desktop.png` through the configured screenshot path when
  Scenario screenshot evidence is enabled.
- Treat viewport capture alone as insufficient if the Maya process is not in the interactive desktop.
- Keep Visual Evidence enabled for CI proof unless a Scenario explicitly does not require it.
- Screenshot capture writes PNG artifacts. Recording capture writes MP4
  artifacts with Crabbox-like defaults of 10 seconds at 15 fps.

Doctor layer:

- `visual-evidence`: screenshot or recording capture is unavailable through the
  Session Broker or host prerequisites.

### Desktop Control

- `maya-stall control click` sends one explicit full-desktop coordinate click
  through the selected Session Broker.
- Use `--dry-run` to verify Host Config, Target Profile, Maya Host, runtime,
  and coordinates without sending input.
- Real SSH hosts with `broker.type: gg-mayasessiond` use the same short-lived
  `/IT` scheduled-task path as desktop Visual Evidence, plus `user32.dll`
  cursor and mouse APIs.
- Host-specific details such as SSH, Windows desktop login, `workRoot`, and
  broker paths stay in Host Config and Session Broker setup.
- When a Fresh Run or Kept Session already owns the Host Lock, use
  `maya-stall attach <run-id> screenshot` and
  `maya-stall attach <run-id> control click --x <pixels> --y <pixels>`.
  These commands verify the Host Lock is owned by that run id before they touch
  the desktop. Standalone `screenshot` and `control click` still fail closed
  for unrelated callers while the host is locked.
- Safe modal handling flow: keep the blocked run active or retained, capture a
  run-scoped screenshot, inspect the current desktop evidence, send one
  explicit coordinate click only when the target is clear, then capture another
  run-scoped screenshot or check run status before allowing the Scenario to
  continue. Do not use out-of-band scheduled tasks for normal modal handling.
- The protected live proof gate runs an opt-in controlled prompt fixture on the
  interactive desktop, captures a full-desktop screenshot, and clears the prompt
  with `maya-stall control click`. This proves the coordinate control path
  without depending on a consuming repo, plugin dialog, or `gg_mayasessiond`
  mutation.

Doctor layer:

- `desktop-control`: explicit desktop click support is unavailable through the
  selected Session Broker or blocked by Host Lock/session-broker health.

### Evidence Store

- Prepare a filesystem Evidence Store path for published Evidence Bundles when Review Comments need durable links.
- Configure a `baseUrl` that reviewers can open for files copied to the Evidence Store.
- Ensure CI or the run user can copy bundles to the destination without embedding credentials in Repo Run Config.
- Keep collection and publishing separate: a run should produce a local Evidence Bundle before anything is copied to the Evidence Store.

Doctor layer:

- Evidence Store publishing is checked by `maya-stall evidence publish`, not by the default fake/local `doctor` path.

### Review Comments

- Provide GitHub or GitLab token material through exact environment variables in CI or local operator config.
- Use `maya-stall review-comment ... --dry-run` when checking markdown without network writes.
- Ensure Review Comments link the published Evidence Bundle rather than local-only paths.

Doctor layer:

- Review Comment credentials and network writes are outside the default Host Health path. Failures belong to `maya-stall review-comment`.

### Host Lock And Retention

- Allow one active Fresh Run per Maya Host.
- Inspect Kept Sessions before clearing Host Locks manually.
- Use Stop Policy intentionally: cleanup after success, keep on failure for debugging, and stop Kept Sessions when done.
- Keep Session Broker `status`, `report`, `stop`, and remote workspace cleanup working for retained runs. `maya-stall status --run <id>` checks broker truth, `attach <id>` prints local run evidence plus broker report data, and `stop <id>` only releases the Host Lock after broker stop and cleanup succeed.
- If a broker session disappears outside Maya Stall, `status --run <id>` reports stale/orphaned state. Use that signal to decide whether manual host cleanup is needed before clearing locks.

Doctor layer:

- `host-lock`: selected Maya Host is locked by an active or unreadable run state.

### Scenario Inputs

- Make sure each Scenario declares only the Run Payload paths it needs.
- Include Plugin Artifacts, Maya Scripts, scenes, Expected Outputs, and include paths explicitly.
- Keep consuming-repo domain assertions in Scenario scripts or Scenario Results, not in Maya Stall generic Validators.

Doctor layer:

- `scenario-inputs`: Repo Run Config references missing, invalid, or unsafe Scenario payload paths.

## Quick Doctor Map

- `local-config`: run `maya-stall init` or fix Repo Run Config schema.
- `target-profile`: see [Target Profile And Host Pool](#target-profile-and-host-pool).
- `host-pool`: see [Target Profile And Host Pool](#target-profile-and-host-pool).
- `host`: see [Target Profile And Host Pool](#target-profile-and-host-pool).
- `fake-ssh`: see [OpenSSH Reachability](#openssh-reachability).
- `ssh`: see [OpenSSH Reachability](#openssh-reachability).
- `work-root`: see [Work Root](#work-root).
- `session-broker`: see [Interactive Desktop](#interactive-desktop) and [Session Broker](#session-broker).
- `maya-version`: see [Autodesk Maya](#autodesk-maya).
- `visual-evidence`: see [Visual Evidence](#visual-evidence).
- `host-lock`: see [Host Lock And Retention](#host-lock-and-retention).
- `scenario-inputs`: see [Scenario Inputs](#scenario-inputs).

## Opt-In Live Smoke

Default tests never require SSH secrets or a real Windows host. To smoke the real SSH doctor path, set only these exact env vars:

```sh
MAYA_STALL_SMOKE_HOST_CONFIG=/path/to/ci-hosts.yaml go test ./internal/cli -run TestOptInRealSSHDoctorSmoke -count=1
```

To run the full live smoke, use:

```sh
MAYA_STALL_SMOKE_HOST_CONFIG=/path/to/ci-hosts.yaml go test ./internal/cli -run 'TestOptInRealSSH(Doctor|Run)Smoke' -count=1
```

`TestOptInRealSSHRunSmoke` first runs `doctor --scenario smoke`, then runs one generated `smoke` Scenario through the configured `gg_mayasessiond` Session Broker, requires screenshot and recording Visual Evidence, and checks that the local Evidence Bundle contains `evidence.json`, events, logs, Scenario Result, the captured screenshot, and an MP4 recording with duration/FPS plus selected Target Profile and Maya Host metadata. `TestOptInRealRunScopedDesktopOpsSmoke` keeps a failed run locked, proves standalone screenshot fails closed while the Host Lock is held, captures a run-scoped desktop screenshot, and clears a controlled modal through `attach <run-id> control click`. Retained-stop live smokes restart the documented interactive `MayaStallSessiondUI` task afterward, or the task named by `MAYA_STALL_SMOKE_SESSIOND_UI_TASK`.

To include the canonical Consuming Repo smoke, add a checked-out consuming repo path and run:

```sh
MAYA_STALL_SMOKE_HOST_CONFIG=/path/to/ci-hosts.yaml MAYA_STALL_CONSUMING_REPO_SMOKE_DIR=/path/to/consuming-repo go test ./internal/cli -run 'TestOptInRealSSH(Doctor|Run|ConsumingRepo)Smoke' -count=1
```

`TestOptInRealSSHConsumingRepoSmoke` builds a temporary Plugin Artifact from the consuming repo checkout, stages it through Maya Stall, imports its plugin module in Maya Python, creates one minimal Maya object interaction, writes plugin-specific Scenario Result JSON, captures screenshot Visual Evidence, and publishes the Evidence Bundle to a temporary filesystem Evidence Store.

All live smoke tests assert the Host Health report before accepting proof. Required live proof must show `ssh-sessiond`, `gg-mayasessiond`, interactive desktop readiness, and Visual Evidence readiness.

To prove live desktop Visual Evidence, run:

```sh
MAYA_STALL_SMOKE_HOST_CONFIG=/path/to/ci-hosts.yaml MAYA_STALL_SMOKE_TARGET_PROFILE=ci MAYA_STALL_SMOKE_HOST=maya-win-01 go test ./internal/cli -run TestOptInRealVisualEvidenceSmoke -count=1
```

`TestOptInRealVisualEvidenceSmoke` selects the configured host through Host Config, Target Profile, and optional Maya Host id. It runs Host Health first, invokes the public `maya-stall record` command, asserts `maya.exe` is running in the interactive Windows `Console` session, validates the command's local Evidence Bundle has one real MP4 recording with duration/FPS and selected-host metadata, and adds a desktop PNG only for the sanitized downloadable proof artifact. The paired `TestOptInRealSSHRunSmoke` validates a recording-enabled Scenario. The machine running the test must have `ffmpeg` on `PATH` to encode the MP4 from captured Windows desktop frames. A skipped test, missing host config, fake runtime, viewport-only screenshot, missing recording, fake MP4 bytes, or non-Console Maya process is not acceptable live proof.

Optional:

- `MAYA_STALL_SMOKE_TARGET_PROFILE`: Target Profile; default `default`.
- `MAYA_STALL_SMOKE_HOST`: pinned Maya Host id.
- `MAYA_STALL_CONSUMING_REPO_SMOKE_DIR`: local checkout path for the canonical Consuming Repo smoke.

## Not The CLI's Job

- Installing OpenSSH Server.
- Installing or licensing Autodesk Maya.
- Creating Windows users, auto-logon, or service wrappers.
- Creating long-lived scheduled tasks other than the prepare script's
  interactive `MayaStallSessiondUI` task. Visual Evidence and desktop control
  commands may create short-lived `/IT` tasks and remove them after the action.
- Storing host credentials, private hostnames, SSH identities, or license data.
- Installing `gg_mayasessiond`.
- Creating network shares or Evidence Store hosting.
- Managing SSH keys, GitHub tokens, GitLab tokens, or Windows credentials.
- Bootstrapping Crabbox-managed cloud machines.

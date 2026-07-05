# Windows Maya Host Setup

Read when preparing a Maya Host, diagnosing `maya-stall doctor` failures, or checking that a Target Profile can run a real Maya UI Session.

This guide is a host-admin checklist. Maya Stall diagnoses these prerequisites but does not install or configure them. The CLI should report the failing Host Health layer and point back here; the Windows machine, license, accounts, SSH identity, Session Broker, work roots, and Evidence Store access remain host-managed.

Maya Stall uses Crabbox as a reference for static SSH, leases, desktop evidence, and artifact publishing, but it is not Crabbox managed cloud bootstrap. Crabbox-managed Windows leases may create and auto-logon a desktop user. Maya Stall v1 targets owned Windows Maya Hosts, so setup happens before `maya-stall run`.

## Checklist

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
        workRoot: C:/maya-stall
        broker: ok
        mayaVersions: ["2025"]
        visualEvidence: true
```

Doctor layer:

- `fake-ssh`: fake/local SSH reachability status would fail for deterministic default tests.
- `ssh`: real SSH reachability failed for a `transport: ssh` Maya Host. Repair host networking, SSH service state, keys, or host config before retrying.

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
- Keep host-specific paths in host-managed config; do not bake them into consuming repos.
- Configure the broker as a structured host-config block:

```yaml
broker:
  type: gg-mayasessiond
  stateDir: C:/maya-stall/sessiond-ui
  python: C:/maya-stall/sessiond-venv311/Scripts/python.exe
  repo: C:/PROJECTS/GG/GG_MayaSessiond
  mcpSource: C:/PROJECTS/GG/GG_MayaMCP
```

Maya Stall invokes `gg_maya_sessiond.cli` on the Windows host through the same SSH transport. Runs stage declared payloads under `workRoot/runs/<run-id>/`, execute a staged wrapper with `script.execute`, download declared outputs from the remote workspace, and capture screenshots with `viewport.capture`.

`maya-stall doctor` also performs live broker probes for `gg_mayasessiond`: it runs daemon `doctor` and `status`, checks the Windows `maya.exe` session, stages a tiny probe script under `workRoot/runs/doctor-*`, executes it with `script.execute`, removes that probe directory, and checks `viewport.capture`. The local Host Lock gates these probes for Maya Stall runs from the same checkout, but operators should still treat doctor as a live diagnostic that briefly executes code in the active Maya session.

Doctor layer:

- `session-broker`: broker unreachable, unhealthy, misconfigured, stale, missing `maya.exe`, or launching Maya in Windows Services session `0` instead of the interactive desktop.

### Visual Evidence

- Confirm the Session Broker can capture screenshots and recordings from the same desktop session as the Maya UI Session.
- Store screenshots, recordings, logs, Scenario Results, and declared outputs in each Evidence Bundle.
- Treat viewport capture alone as insufficient if the Maya process is not in the interactive desktop.
- Keep Visual Evidence enabled for CI proof unless a Scenario explicitly does not require it.

Doctor layer:

- `visual-evidence`: screenshot or recording capture is unavailable through the Session Broker.

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

Optional:

- `MAYA_STALL_SMOKE_TARGET_PROFILE`: Target Profile; default `default`.
- `MAYA_STALL_SMOKE_HOST`: pinned Maya Host id.

## Not The CLI's Job

- Installing OpenSSH Server.
- Installing or licensing Autodesk Maya.
- Creating Windows users, auto-logon, scheduled tasks, or service wrappers.
- Installing `gg_mayasessiond`.
- Creating network shares or Evidence Store hosting.
- Managing SSH keys, GitHub tokens, GitLab tokens, or Windows credentials.
- Bootstrapping Crabbox-managed cloud machines.

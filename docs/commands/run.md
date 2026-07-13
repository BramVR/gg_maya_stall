# run

`maya-stall run` runs a named Scenario and writes an Evidence Bundle.

```sh
maya-stall run smoke
maya-stall run --host-config ci-hosts.yaml --target-profile ci smoke
maya-stall run --host-config ci-hosts.yaml --target-profile ci --host maya-win-01 smoke
maya-stall run --host-lock-wait 30s smoke
maya-stall run --host-lock-fail-fast smoke
maya-stall run --keep-on-failure smoke
maya-stall run --stop-after success smoke
maya-stall run --stop-after failure smoke
maya-stall run --stop-after always smoke
maya-stall run --stop-after never smoke
```

## Behavior

The command calls the Fresh Run lifecycle, which owns this setup, execute, and
settle flow:

1. Load Repo Run Config.
2. Select and normalize the named Scenario.
3. Resolve Target Profile, Host Pool, and Maya Host from host config if
   provided.
4. Resolve the Host/Broker runtime contract.
5. Acquire a Host Lock.
6. Ask the Session Broker to stop any inherited Maya UI Session and start a new identified Maya UI Session.
7. Stage only declared Run Payload paths.
8. Provide `MAYA_STALL_SCENARIO_RESULT` to the Scenario.
9. Run through the resolved fake-local or ssh-sessiond runtime.
10. Collect outputs, logs, runtime metadata, broker session identity, Scenario Result, and Visual Evidence into an
   Evidence Bundle.
11. Run Validators.
12. Apply the Stop Policy to that Maya UI Session and release or retain the Host Lock.

Supported runtime profiles:

- `fake-local`: fake Maya Host with the fake Session Broker.
- `ssh-sessiond`: SSH Windows Maya Host with `broker.type: gg-mayasessiond`.

An SSH Maya Host without structured `gg_mayasessiond` broker config fails before
payload staging. SSH Host plus fake broker, fake Host plus real broker, and
malformed broker config are not silently downgraded.

With `broker.type: gg-mayasessiond`, `run` stages declared payloads under
`workRoot/runs/<run-id>/`, writes a small Scenario wrapper into the remote
workspace, executes it through `gg_maya_sessiond.cli call ... script.execute`,
downloads declared outputs, and captures configured Visual Evidence from the
interactive Windows desktop session.
Before staging payloads, `run` checks the broker status for commandPort
readiness. If the commandPort layer is unhealthy, it restarts the configured
interactive recovery task (`broker.recoveryTask`, default
`MayaStallSessiondUI`) and retries the status preflight; if recovery is
unavailable or still unhealthy, the command fails at the `session-broker`
preflight layer instead of starting a Scenario.
If the selected Maya Host config sets `trustedPluginArtifactsRoot`, `run` also
copies declared `pluginArtifacts` to that stable host-managed root and exposes
it to Scenario scripts as `MAYA_STALL_TRUSTED_PLUGIN_ARTIFACTS_ROOT`.
Before staging Plugin Artifacts, `run` validates that Maya's durable
`SafeModeAllowedlistPaths` preference for the selected Maya version contains
the configured root, declared Plugin Artifact destinations, and parent
directories for nested `.mll` and `.py` files under directory artifacts. Python
files are treated conservatively because Maya plug-in callbacks can be
published dynamically. A missing baseline fails before upload or Scenario execution, with
an actionable TrustCenter diagnostic instead of hanging behind Maya's
untrusted plug-in modal.
The normal per-run payload copy still happens.
Remote Scenario execution through `script.execute` is capped at 10 minutes.
If `script.execute` or equivalent broker execution fails after the remote
workspace exists, `run` first attempts best-effort collection of declared
outputs, logs, screenshots, and any available Scenario Result JSON into the
local Evidence Bundle. If the collected Scenario Result is valid, explicitly
`passed`, and configured Validators pass against the collected outputs, `run`
accepts the Scenario as completed and exits 0 even though the broker return path
failed. Missing, malformed, failed, incomplete, or Validator-failing collected
results remain failed and exit non-zero.
When Scenario screenshot evidence is enabled and the selected broker supports
screenshot Visual Evidence, `run` also best-effort captures
`screenshots/failure-desktop.png` for unrecovered failures.
`manifest.json` and `evidence.json` record the resolved runtime profile, host
adapter, broker adapter, broker config source, and live-proof eligibility.
Scenario normalization owns Run Payload paths, Expected Outputs, evidence
policy, and Validator config, so local run validation, Doctor, SSH output
downloads, and Evidence Bundle output discovery use the same paths.

## Run Workspace Layout

For each run, Maya Stall derives local and remote paths from one Run Workspace:

- local state: `.maya-stall/state/runs/<run-id>/`
- local staged payload mirror: `.maya-stall/state/runs/<run-id>/payload/`
- local workspace: `.maya-stall/state/runs/<run-id>/workspace/`
- local Evidence Bundle: `artifacts/maya-stall/<run-id>/`
- remote run root: `workRoot/runs/<run-id>/`
- remote staged payloads: `workRoot/runs/<run-id>/payload/`
- remote workspace: `workRoot/runs/<run-id>/workspace/`
- remote Scenario Result: `workRoot/runs/<run-id>/workspace/<expectedOutputs.scenarioResult>`
- optional trusted Plugin Artifact root: `trustedPluginArtifactsRoot/<repo-relative-plugin-path>`

## Trusted Plugin Artifacts

Autodesk Maya secure plug-in loading is location-based. Trusting each fresh
`workRoot/runs/<run-id>` path can trigger a new prompt every run, while trusting
all of `workRoot` or `workRoot/runs` would also trust arbitrary run payloads.

Use this split instead:

- Consuming repo: declare Plugin Artifacts in `payload.pluginArtifacts` and
  keep Scenario scripts responsible for loading and asserting the plug-in.
- Operator/host config: set `trustedPluginArtifactsRoot` to a stable directory
  using an absolute Windows drive or UNC path that is not inside or above
  `workRoot/runs`, then trust that root plus the declared destination and
  nested plug-in parent directories reported by `doctor --scenario <scenario>`
  for the Windows account that runs the interactive Maya UI.
- Scenario script: when `MAYA_STALL_TRUSTED_PLUGIN_ARTIFACTS_ROOT` is present,
  load the declared plug-in from that root using the same repo-relative path;
  otherwise load from the per-run `payload/pluginArtifacts` path.

Example Scenario-side path selection:

```python
import os
from pathlib import Path

trusted_root = os.environ.get("MAYA_STALL_TRUSTED_PLUGIN_ARTIFACTS_ROOT")
if trusted_root:
    plugin_path = Path(trusted_root) / "build" / "demo.mll"
else:
    plugin_path = Path.cwd().parent / "payload" / "pluginArtifacts" / "build" / "demo.mll"
```

Maya Stall removes each declared destination in the trusted root before copying
the current Plugin Artifact, so directory artifacts do not retain stale files.
It adds trusted locations to Maya preferences only through the explicit
`maya-stall doctor --scenario <scenario> --repair-trusted-plugin-allowlist`
operator action; ordinary run paths validate but do not mutate host security
policy.

Default output:

```text
artifacts/maya-stall/<run-id>/
```

## Host Locking

One active Fresh Run may use a Maya Host at a time. Real SSH hosts store the
authoritative lock at `workRoot/state/locks/host.lock`, so independent
controllers and repo checkouts contend on the same host-owned state. Each
active lock binds a unique token to its Run ID and renews a bounded lease while
the controller is alive. A kept run has no expiring lease and remains locked
until `maya-stall stop <run-id>` verifies ownership and stops it.

An expired lease is recoverable only after the configured Session Broker proves
that no Maya UI Session is active. Unreadable ownership, a live lease, or an
unavailable/active broker fails closed. The repo-local lock remains as a mirror
for local commands and migration, but it is not the authority for an SSH host.

Use `--host-lock-wait <duration>` to wait for a busy host or
`--host-lock-fail-fast` to fail immediately.

## Stop Policy

Fresh Runs stop their identified broker-owned Maya UI Session and clean hidden
run state plus the remote run workspace by default after writing the Evidence
Bundle. Use `--keep-on-failure` to retain a
failed Session Broker-backed Maya UI Session for debugging.

Explicit `--stop-after` values are:

- `success`: stop after successful runs.
- `failure`: stop after failed runs.
- `always`: always stop.
- `never`: keep the session until `maya-stall stop`.

Kept sessions write a hidden Run Record under `.maya-stall/state/runs/<run-id>/`
with the local paths, remote workspace, broker adapter, broker capabilities, and
remote session metadata needed by `status`, `attach`, and `stop`.

Kept sessions are visible through truth-seeking `status`, readable through
read-only `attach`, and cleaned with broker-backed `stop`.

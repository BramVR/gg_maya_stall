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
6. Stage only declared Run Payload paths.
7. Provide `MAYA_STALL_SCENARIO_RESULT` to the Scenario.
8. Run through the resolved fake-local or ssh-sessiond runtime.
9. Collect outputs, logs, runtime metadata, Scenario Result, and Visual Evidence into an
   Evidence Bundle.
10. Run Validators.
11. Apply the Stop Policy and release or retain the Host Lock.

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
Remote Scenario execution through `script.execute` is capped at 10 minutes.
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

Default output:

```text
artifacts/maya-stall/<run-id>/
```

## Host Locking

One active Fresh Run may use a Maya Host at a time.

Use `--host-lock-wait <duration>` to wait for a busy host or
`--host-lock-fail-fast` to fail immediately.

## Stop Policy

Fresh Runs stop and clean hidden run state and the remote run workspace by
default after writing the Evidence Bundle. Use `--keep-on-failure` to retain a
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

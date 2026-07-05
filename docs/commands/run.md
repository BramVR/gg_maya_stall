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

The run flow is:

1. Load Repo Run Config.
2. Select the named Scenario.
3. Resolve Target Profile, Host Pool, and Maya Host from host config if
   provided.
4. Acquire a Host Lock.
5. Stage only declared Run Payload paths.
6. Provide `MAYA_STALL_SCENARIO_RESULT` to the Scenario.
7. Run through fake/local transport or opt-in real SSH transport.
8. Collect outputs, logs, metadata, Scenario Result, and Visual Evidence into an
   Evidence Bundle.
9. Run Validators.
10. Apply the Stop Policy and release or retain the Host Lock.

With `broker.type: gg-mayasessiond`, `run` stages declared payloads under
`workRoot/runs/<run-id>/`, writes a small Scenario wrapper into the remote
workspace, executes it through `gg_maya_sessiond.cli call ... script.execute`,
downloads declared outputs, and captures screenshots through `viewport.capture`.
Remote Scenario execution through `script.execute` is capped at 10 minutes.

Default output:

```text
artifacts/maya-stall/<run-id>/
```

## Host Locking

One active Fresh Run may use a Maya Host at a time.

Use `--host-lock-wait <duration>` to wait for a busy host or
`--host-lock-fail-fast` to fail immediately.

## Stop Policy

Fresh Runs stop and clean hidden run state by default after writing the Evidence
Bundle. Use `--keep-on-failure` to retain a failed session for debugging.

Explicit `--stop-after` values are:

- `success`: stop after successful runs.
- `failure`: stop after failed runs.
- `always`: always stop.
- `never`: keep the session until `maya-stall stop`.

Kept sessions are visible through `status`, readable through `attach`, and
cleaned with `stop`.

# status

Unscoped `maya-stall status` lists kept sessions that still hold Host Locks.
`maya-stall status --run <run-id>` shows one run's current or durable state.

```sh
maya-stall status
maya-stall status --run <run-id>
maya-stall status --json --run <run-id>
maya-stall status --control-plane https://maya-stall.example.com --json --run <run-id>
```

`--json` returns the stable versioned status object used by both Embedded Mode
and Configured Control Plane Mode. A configured read requires `--run` and an
origin-only HTTPS Control Plane URL. Authentication defaults to the token in
`MAYA_STALL_CONTROL_PLANE_TOKEN`; `--control-plane-token-env <name>` selects a
different environment variable.

Use it after `--keep-on-failure` or `--stop-after never` to find sessions that
still hold Host Locks. Kept run status includes the resolved runtime profile,
host adapter, broker adapter, live-proof eligibility, retention reason, local
state path, remote workspace, and broker session id recorded at run time.

For broker-backed runs, status is truth-seeking: it reads the local Run Record,
then asks the Session Broker whether the retained Maya UI Session still exists.
If the broker session disappeared or changed, status reports `state: stale`
instead of pretending local state is enough.

Completed and failed Run IDs remain queryable from the embedded Run Ledger
after transient state is cleaned and until configured ledger retention expires
them. Their status includes Scenario, Target Profile, Maya Host, result status,
acceptance time, and Evidence Bundle path.

Kept Sessions remain visible until `maya-stall stop <run-id>` performs
broker-backed cleanup, removes local run state, and releases the Host Lock.
The same Run ID then transitions to `completed`, `failed`, or `cleanup-failed`
in durable history.

Use [events](events.md), [logs](logs.md), and [result](result.md) for the other
durable run views.

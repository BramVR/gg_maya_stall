# status

Unscoped `maya-stall status` lists kept sessions that still hold Host Locks.
`maya-stall status --run <run-id>` shows one run's current or durable state.

```sh
maya-stall status
maya-stall status --run <run-id>
```

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

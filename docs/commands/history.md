# history

`maya-stall history` lists accepted runs from the durable embedded Run Ledger.

```sh
maya-stall history
maya-stall history --json
maya-stall history --scenario smoke
maya-stall history --host maya-win-01 --state failed
maya-stall history --since 24h
maya-stall history --since 2026-07-14T08:00:00Z
```

History remains available after transient Run State is cleaned. Records are
newest first and can be filtered by exact Scenario, Maya Host, terminal state,
or acceptance time. States are `submitted`, `completed`, `failed`, `kept`, and
`cleanup-failed`.

`--json` returns one versioned object with a `runs` array. Each run identifies
its retained event and log paths. Ordered JSONL events carry `sequence`,
`timestamp`, `phase`, `type`, `stream`, and structured `details` fields.

The embedded log and event stream are bounded by count and bytes. If a limit is
exceeded, Maya Stall inserts an explicit truncation marker and preserves the
newest retained data. Configured limits are capped at 100,000 events, 64 MiB of
retained event data, and 64 MiB of retained log data. Evidence Bundle files are
not truncated by this policy.

History retention applies only to terminal `completed` and `failed` ledger
records. It never deletes local or published Evidence Bundles, and unresolved
`kept` or `cleanup-failed` records are not expired automatically.
If Repo Run Config is missing, the 30-day default applies. If config exists but
cannot be trusted or its ledger policy is invalid, `history` skips destructive
retention while still listing durable records.

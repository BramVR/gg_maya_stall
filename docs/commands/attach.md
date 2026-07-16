# attach

`maya-stall attach` prints a run's events, logs, and Evidence Bundle path.
Ledger-only output also includes the durable run state. For kept or
cleanup-pending live runs, attach includes Session Broker observation data.
Run-scoped subcommands can
also capture one desktop screenshot or send one explicit coordinate click while
the requested active or kept run owns the Host Lock.

```sh
maya-stall attach <run-id>
maya-stall attach <run-id> --control-plane https://maya-stall.example.com --from-sequence 1
maya-stall attach <run-id> screenshot
maya-stall attach <run-id> control click --x 960 --y 540
```

Plain attach is read-only. It does not reuse the Maya UI Session for execution
and it does not open an interactive desktop viewer.

With `--control-plane`, plain attach authenticates and follows the active Run
Ledger from the inclusive `--from-sequence` cursor. It prints each durable event
once in increasing sequence order. An `events-truncated` record reports a gap
before retained history. A bounded `stream-truncated` record prints the next
cursor and exits cleanly so the caller can reconnect. At `stream-end`, attach
reads retained logs and the result, including final Evidence and cleanup state.
The default token environment is `MAYA_STALL_CONTROL_PLANE_TOKEN`;
`--control-plane-token-env` selects another name.

Use it when a kept run failed and you need the event stream before deciding
whether to collect more evidence or stop the session.

For completed, failed, or durable-only cleanup-failed embedded runs, plain
attach reads bounded events and logs from the Run Ledger even after transient
Run State is gone. A cleanup-failed run that still owns a live session uses its
transient Run State and Session Broker instead. Retained logs and events contain
explicit markers when configured limits truncate older content. The original
Evidence Bundle remains independent.

Use `attach <run-id> screenshot` when a real UI run is blocked by a modal and
the ordinary standalone `screenshot` command correctly refuses to select the
locked Maya Host. The screenshot is captured through the selected run's Session
Broker and written into `artifacts/maya-stall/<run-id>/screenshots/`.

Use `attach <run-id> control click --x <pixels> --y <pixels>` only after
reviewing current desktop evidence. The click is a single full-desktop
coordinate click through the selected run's Session Broker.

After a screenshot or click succeeds, a later transient, Evidence, or ledger
persistence failure is reported as `durabilityWarning` while the command still
reports the completed action. Do not retry a non-idempotent click; inspect
transient/Evidence artifacts and use `stop` to attempt another bounded ledger
sync.

Run-scoped screenshot and control fail closed unless the Host Lock for the
run's Maya Host belongs to that same active or kept run id. They do not search
for another unlocked host and they do not let unrelated callers bypass a locked
run.

If a kept or cleanup-pending live run's broker cannot provide attach/log
observation, `attach` fails explicitly instead of faking an interactive attach
from local files alone.

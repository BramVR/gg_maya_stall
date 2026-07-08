# attach

`maya-stall attach` prints a kept run's events, logs, Scenario Result, Evidence
Bundle path, and Session Broker observation data. Run-scoped subcommands can
also capture one desktop screenshot or send one explicit coordinate click while
the requested active or kept run owns the Host Lock.

```sh
maya-stall attach <run-id>
maya-stall attach <run-id> screenshot
maya-stall attach <run-id> control click --x 960 --y 540
```

Plain attach is read-only. It does not reuse the Maya UI Session for execution
and it does not open an interactive desktop viewer.

Use it when a kept run failed and you need the event stream before deciding
whether to collect more evidence or stop the session.

Use `attach <run-id> screenshot` when a real UI run is blocked by a modal and
the ordinary standalone `screenshot` command correctly refuses to select the
locked Maya Host. The screenshot is captured through the selected run's Session
Broker and written into `artifacts/maya-stall/<run-id>/screenshots/`.

Use `attach <run-id> control click --x <pixels> --y <pixels>` only after
reviewing current desktop evidence. The click is a single full-desktop
coordinate click through the selected run's Session Broker.

Run-scoped screenshot and control fail closed unless the Host Lock for the
run's Maya Host belongs to that same active or kept run id. They do not search
for another unlocked host and they do not let unrelated callers bypass a locked
run.

If a broker cannot provide attach/log observation, `attach` fails explicitly
instead of faking an interactive attach from local files alone.

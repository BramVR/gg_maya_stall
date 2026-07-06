# attach

`maya-stall attach` prints a kept run's events, logs, Scenario Result, Evidence
Bundle path, and Session Broker observation data.

```sh
maya-stall attach <run-id>
```

Attach is read-only. It does not reuse the Maya UI Session for execution and it
does not open an interactive desktop viewer.

Use it when a kept run failed and you need the event stream before deciding
whether to collect more evidence or stop the session.

If a broker cannot provide attach/log observation, `attach` fails explicitly
instead of faking an interactive attach from local files alone.

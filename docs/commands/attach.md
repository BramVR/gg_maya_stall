# attach

`maya-stall attach` prints a kept run's events and Session Broker log.

```sh
maya-stall attach <run-id>
```

Attach is read-only. It does not reuse the Maya UI Session for execution and it
does not open an interactive desktop viewer.

Use it when a kept run failed and you need the event stream before deciding
whether to collect more evidence or stop the session.

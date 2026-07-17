# stop

`maya-stall stop` stops a kept run and releases its Host Lock.

```sh
maya-stall stop <run-id>
maya-stall stop --control-plane https://maya-stall.example.com <run-id>
```

Use it after debugging a kept session from `--keep-on-failure` or
`--stop-after never`.

Stop is authoritative cleanup. For broker-backed runs, it asks the Session
Broker to stop the retained Maya UI Session, removes the retained remote run
workspace when supported, then removes local run state under
`.maya-stall/state/` and makes the Maya Host selectable for the next Fresh Run.

If the broker stop or cleanup operation fails, local run state and the Host Lock
remain in place so the command does not lie about cleanup.

Configured `stop` is deliberately narrower: it cancels only a Run that is
still queued. Cancellation durably records `run.canceled`, produces terminal
status and minimal Evidence, and never acquires or releases a Host Lock. If
assignment has started, the Control Plane rejects cancellation rather than
touching the Maya Host. Authentication uses the same Control Plane bearer-token
options as `run` and `status`.

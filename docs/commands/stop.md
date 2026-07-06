# stop

`maya-stall stop` stops a kept run and releases its Host Lock.

```sh
maya-stall stop <run-id>
```

Use it after debugging a kept session from `--keep-on-failure` or
`--stop-after never`.

Stop is authoritative cleanup. For broker-backed runs, it asks the Session
Broker to stop the retained Maya UI Session, removes the retained remote run
workspace when supported, then removes local run state under
`.maya-stall/state/` and makes the Maya Host selectable for the next Fresh Run.

If the broker stop or cleanup operation fails, local run state and the Host Lock
remain in place so the command does not lie about cleanup.

# status

`maya-stall status` shows kept run state.

```sh
maya-stall status
maya-stall status --run <run-id>
```

Use it after `--keep-on-failure` or `--stop-after never` to find sessions that
still hold Host Locks.

Kept Sessions remain visible until `maya-stall stop <run-id>` removes their run
state and releases the Host Lock.

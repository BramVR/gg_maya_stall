# stop

`maya-stall stop` stops a kept run and releases its Host Lock.

```sh
maya-stall stop <run-id>
```

Use it after debugging a kept session from `--keep-on-failure` or
`--stop-after never`.

Stop removes kept run state under `.maya-stall/state/` and makes the Maya Host
selectable for the next Fresh Run.

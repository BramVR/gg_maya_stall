# record

`maya-stall record` captures a standalone recording artifact through the Session
Broker.

```sh
maya-stall record
maya-stall record --host-config ci-hosts.yaml --target-profile ci
maya-stall record --host-config ci-hosts.yaml --target-profile ci --host maya-win-01
```

Normal recording defaults follow the selected Crabbox timing slice: 10 seconds
at 15 fps. Other timing defaults remain decision records until implemented.

The fake Session Broker supports recordings. With
`broker.type: gg-mayasessiond`, recording reports an actionable unsupported
error until the daemon exposes a recording tool.

The command writes a local Evidence Bundle under `artifacts/maya-stall/`.

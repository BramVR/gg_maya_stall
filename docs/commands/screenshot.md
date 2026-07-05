# screenshot

`maya-stall screenshot` captures a standalone screenshot artifact through the
Session Broker.

```sh
maya-stall screenshot
maya-stall screenshot --host-config ci-hosts.yaml --target-profile ci
maya-stall screenshot --host-config ci-hosts.yaml --target-profile ci --host maya-win-01
```

Default commands use the fake Session Broker. Real capture depends on host
config, an interactive desktop, and Session Broker support. With
`broker.type: gg-mayasessiond`, screenshots are captured through
`viewport.capture`.

The command writes a local Evidence Bundle under `artifacts/maya-stall/`.

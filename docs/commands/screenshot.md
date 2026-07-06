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
an interactive Windows scheduled task using desktop APIs, not raw SSH desktop
capture or `viewport.capture`. SSH Maya Hosts must use the `ssh-sessiond`
runtime profile; they do not fall back to fake screenshot capture when broker
config is missing or malformed.

The command writes a local Evidence Bundle under `artifacts/maya-stall/` with
the resolved runtime metadata. `evidence.json` records the artifact kind,
relative path, media type, selected Target Profile, and selected Maya Host.

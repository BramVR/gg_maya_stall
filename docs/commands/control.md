# control

`maya-stall control click` sends an explicit desktop click through the selected
Session Broker.

```sh
maya-stall control click --x 960 --y 540
maya-stall control click --x 960 --y 540 --dry-run
maya-stall control click --x 960 --y 540 --host-config ci-hosts.yaml --target-profile ci
maya-stall control click --x 960 --y 540 --host-config ci-hosts.yaml --target-profile ci --host maya-win-01
```

The command resolves the same Host Config, Target Profile, Maya Host, Host Lock,
and runtime metadata as `screenshot` and `record`. Output records the action,
selected target, runtime, coordinates, and dry-run state.

Default commands use the fake Session Broker. Real SSH hosts with
`broker.type: gg-mayasessiond` run the click through a short-lived interactive
Windows scheduled task using `user32.dll` desktop APIs. This targets the logged
in desktop session that owns Maya, not the raw SSH session.

Only non-negative pixel coordinates are accepted. Use `--dry-run` to verify the
selected host and coordinates without sending input.

The live proof gate includes a controlled blocking desktop prompt fixture: it
captures a full-desktop screenshot while the prompt is visible, then clears it
with `maya-stall control click`. Named or located UI-element clicks are not part
of this command yet; use explicit coordinates from reviewed desktop evidence.

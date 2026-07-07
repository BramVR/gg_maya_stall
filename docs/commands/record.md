# record

`maya-stall record` captures a standalone MP4 desktop recording artifact through
the Session Broker.

```sh
maya-stall record
maya-stall record --host-config ci-hosts.yaml --target-profile ci
maya-stall record --host-config ci-hosts.yaml --target-profile ci --host maya-win-01
```

Default commands use the fake Session Broker. Real SSH hosts use an interactive
Windows scheduled task to capture desktop frames from the same visible desktop
that owns Maya, download the frame archive, and encode `recordings/recording.mp4`
locally with `ffmpeg`. The default recording duration is 10 seconds at 15 fps.
This is desktop Visual Evidence, distinct from broker viewport capture.

The command writes a local Evidence Bundle under `artifacts/maya-stall/`.
`evidence.json` records the artifact kind, relative path, media type, duration,
FPS, selected Target Profile, and selected Maya Host.

# logs

`maya-stall logs` reads one run's bounded retained log from Embedded Mode or a
Configured Control Plane.

```sh
maya-stall logs <run-id>
maya-stall logs --json <run-id>
maya-stall logs --control-plane https://maya-stall.example.com --json <run-id>
```

JSON is a versioned object with `kind: logs`, the Run ID, retained `content`,
byte count, and explicit `truncated` state.

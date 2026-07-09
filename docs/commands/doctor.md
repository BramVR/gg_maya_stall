# doctor

`maya-stall doctor` checks local config, Target Profile selection, and Host
Health layers before a run. It is fast and diagnostic. The fake/local path does
not create host resources; live `gg_mayasessiond` checks briefly stage, execute,
and remove a probe under the configured work root.

Doctor builds one stable Host Health report before rendering text. The report
records Target Profile, Host Pool, selected Maya Host, runtime profile, layer
statuses, Host Lock state, Session Broker source, interactive desktop proof, and
Visual Evidence source.

```sh
maya-stall doctor
maya-stall doctor --scenario smoke
maya-stall doctor --host-config ci-hosts.yaml --target-profile ci
maya-stall doctor --host-config ci-hosts.yaml --target-profile ci --host maya-win-01
maya-stall doctor --host-config ci-hosts.yaml --target-profile ci --host maya-win-01 --scenario smoke
```

## What It Checks

Doctor reports the layer that failed and a repair hint where possible:

- local repo config and normalized Scenario input shape, including Run Payload
  paths, Expected Outputs, and Validator config;
- Target Profile and Host Pool references;
- pinned Maya Host selection;
- Host/Broker runtime contract (`fake-local` or `ssh-sessiond`);
- fake/local SSH readiness for deterministic default tests;
- real SSH reachability when `transport: ssh` is configured;
- work root readiness;
- Session Broker readiness;
- Maya version compatibility;
- Visual Evidence support;
- Host Lock state.

The `maya-version` layer probes real Windows Maya Hosts over SSH for installed
Autodesk Maya versions in standard Autodesk install directories and registry
install-path entries. It compares the discovered install inventory with the
Scenario's Maya Version Requirement and reports drift when host config declares
different `mayaVersions`. Config declarations are advisory for doctor; they are
not treated as proof that Maya is installed.

Default checks stay fake/local. Real SSH is opt-in through host config outside
the consuming repo. A real SSH Maya Host must configure
`broker.type: gg-mayasessiond`; doctor reports a runtime/session-broker failure
instead of falling back to the fake Session Broker.

With `broker.type: gg-mayasessiond`, doctor runs the daemon `doctor` and
`status` commands, checks that `maya.exe` is in the interactive desktop session,
stages and executes a tiny `script.execute` probe under `workRoot/runs/doctor-*`,
checks `viewport.capture`, runs a short desktop recording capture/MP4 encode
probe under the configured work root, and fails if Maya is running in Windows
Services session `0`. Visual Evidence readiness is reported from those same
Session Broker and recording prerequisites, not from the static host config or
viewport capture alone.

## When To Run

Run doctor before a long workflow, after changing host config, before enabling a
new real host in CI, and when a Scenario fails before entering Maya.

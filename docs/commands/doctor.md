# doctor

`maya-stall doctor` checks local config, Target Profile selection, and Host
Health layers before a run. It is fast, diagnostic, and should not create or
delete host resources.

```sh
maya-stall doctor
maya-stall doctor --scenario smoke
maya-stall doctor --host-config ci-hosts.yaml --target-profile ci
maya-stall doctor --host-config ci-hosts.yaml --target-profile ci --host maya-win-01
maya-stall doctor --host-config ci-hosts.yaml --target-profile ci --host maya-win-01 --scenario smoke
```

## What It Checks

Doctor reports the layer that failed and a repair hint where possible:

- local repo config and Scenario input shape;
- Target Profile and Host Pool references;
- pinned Maya Host selection;
- fake/local SSH readiness for deterministic default tests;
- real SSH reachability when `transport: ssh` is configured;
- work root readiness;
- Session Broker readiness;
- Maya version compatibility;
- Visual Evidence support;
- Host Lock state.

Default checks stay fake/local. Real SSH is opt-in through host config outside
the consuming repo.

## When To Run

Run doctor before a long workflow, after changing host config, before enabling a
new real host in CI, and when a Scenario fails before entering Maya.

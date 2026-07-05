# Windows Maya Host

Read when working on live SSH transport, `gg_mayasessiond`, Session Broker integration, Visual Evidence, recordings, opt-in live smoke tests, or the public Windows Maya Host setup checklist.

Public setup guide: `docs/setup/windows-maya-host.md`.

## Current Dev Fixture

- Host alias: `desktop-mtiofol`
- Alt alias: `maya-win`
- Tailscale IP: `100.75.22.116`
- Windows user: `zo`
- SSH identity on Mac: `~/.ssh/desktop-8fh3ql8`
- SSH config lives in `~/.ssh/config`, not this repo.
- Maya host work root: `C:\maya-stall`
- Runs: `C:\maya-stall\runs`
- Artifacts: `C:\maya-stall\artifacts`
- UI sessiond state dir: `C:\maya-stall\sessiond-ui`
- Non-UI/service test state dir, avoid for UI proof: `C:\maya-stall\sessiond`
- Isolated Python venv for sessiond: `C:\maya-stall\sessiond-venv311`
- `gg_mayasessiond` checkout: `C:\PROJECTS\GG\GG_MayaSessiond`
- `GG_MayaMCP` checkout: `C:\PROJECTS\GG\GG_MayaMCP`
- Maya used for smoke now: `C:\Program Files\Autodesk\Maya2025\bin\maya.exe`

## Important Discovery

Launching Maya from a raw SSH process can start real `maya.exe` in Windows Services session `0`. That is not acceptable for Maya Stall UI testing, even if MCP tools and viewport capture work.

For UI evidence, `maya.exe` must run in the interactive console session for `zo`:

```powershell
tasklist /v /fi "imagename eq maya.exe"
```

Expected signal:

- `Session Name`: `Console`
- `Session#`: `1`
- `User Name`: `DESKTOP-MTIOFOL\ZO`

Wrong signal:

- `Session Name`: `Services`
- `Session#`: `0`

## Current Interactive Launch Method

The current live fixture uses an interactive Windows Scheduled Task:

- Task name: `MayaStallSessiondUI`
- Mode: interactive only
- Launcher: `C:\maya-stall\start-sessiond-ui.cmd`

The launcher starts:

```cmd
C:\maya-stall\sessiond-venv311\Scripts\python.exe -m gg_maya_sessiond.cli start --state-dir C:\maya-stall\sessiond-ui --maya-exe "C:\Program Files\Autodesk\Maya2025\bin\maya.exe" --mcp-python C:\maya-stall\sessiond-venv311\Scripts\python.exe --mcp-src C:\PROJECTS\GG\GG_MayaMCP --mcp-script-dirs C:\maya-stall\runs --wait-timeout-seconds 180 --json
```

Use this only as the current development fixture. The product implementation should model this as Session Broker behavior and keep host-specific paths configurable.

## Quick Checks

SSH:

```sh
ssh desktop-mtiofol 'hostname && whoami'
```

Expected:

```text
DESKTOP-MTIOFOL
desktop-mtiofol\zo
```

Doctor:

```sh
ssh desktop-mtiofol 'powershell -NoProfile -Command "cd C:\PROJECTS\GG\GG_MayaSessiond; & C:\maya-stall\sessiond-venv311\Scripts\python.exe -m gg_maya_sessiond.cli doctor --state-dir C:\maya-stall\sessiond-ui --mcp-src C:\PROJECTS\GG\GG_MayaMCP --json"'
```

Status:

```sh
ssh desktop-mtiofol 'powershell -NoProfile -Command "cd C:\PROJECTS\GG\GG_MayaSessiond; & C:\maya-stall\sessiond-venv311\Scripts\python.exe -m gg_maya_sessiond.cli status --state-dir C:\maya-stall\sessiond-ui --json"'
```

Tool list:

```sh
ssh desktop-mtiofol 'powershell -NoProfile -Command "cd C:\PROJECTS\GG\GG_MayaSessiond; & C:\maya-stall\sessiond-venv311\Scripts\python.exe -m gg_maya_sessiond.cli call --state-dir C:\maya-stall\sessiond-ui --list --json"'
```

Visual proof:

```sh
ssh desktop-mtiofol 'powershell -NoProfile -Command "cd C:\PROJECTS\GG\GG_MayaSessiond; & C:\maya-stall\sessiond-venv311\Scripts\python.exe -m gg_maya_sessiond.cli call --state-dir C:\maya-stall\sessiond-ui viewport.capture format=jpeg width=640 height=360 quality=70 --json | Set-Content -Encoding UTF8 C:\maya-stall\artifacts\ui-viewport-capture.json"'
```

Decode screenshot:

```sh
ssh desktop-mtiofol 'powershell -NoProfile -Command "$j=Get-Content C:\maya-stall\artifacts\ui-viewport-capture.json -Raw | ConvertFrom-Json; [IO.File]::WriteAllBytes(\"C:\maya-stall\artifacts\ui-viewport-capture.jpg\", [Convert]::FromBase64String($j.content[0].data))"'
```

## Current Known Proof

- `C:\maya-stall\artifacts\ui-viewport-capture.jpg`
- `C:\maya-stall\artifacts\ui-viewport-capture.json`
- Capture metadata: JPEG, 640x360, frame 1, `modelPanel4`

## Development Implications

- `doctor` needs an explicit interactive-desktop/session check, not only `maya.exe` found.
- Live smoke tests must assert `maya.exe` is in `Console` session before accepting screenshots or recordings.
- The broker adapter should treat service-session Maya as unhealthy for UI runs.
- Host paths, user, Maya version, scheduled-task strategy, and state dirs must be configurable.
- Keep this host fixture out of default tests; use it only for opt-in live smoke.

## Do Not Assume

- Do not assume raw SSH launch means visible UI.
- Do not assume viewport capture alone proves the process is in the interactive desktop.
- Do not commit SSH keys, Windows credentials, or machine secrets.
- Do not revert unrelated dirty files in `C:\PROJECTS\GG\GG_MayaSessiond`.

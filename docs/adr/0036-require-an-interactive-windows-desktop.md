# Require An Interactive Windows Desktop

Maya Stall v1 will require an interactive logged-in Windows desktop on each Maya Host. Crabbox managed Windows leases create and auto-logon a desktop user, while static hosts are host-managed; Maya Stall uses owned hosts, so setup stays host-managed but doctor must fail clearly when Maya UI or Visual Evidence capture would run only in a service or non-visible SSH session.

Development discovery: launching `maya.exe` from raw SSH can create a real Maya process in Windows Services session `0`. MCP calls and viewport capture may still work there, but that is not accepted as a Maya UI Session. For v1 UI runs, host health and live smoke tests must verify Maya is running in the interactive console session, for example `tasklist /v /fi "imagename eq maya.exe"` showing `Session Name` as `Console` and not `Services`.

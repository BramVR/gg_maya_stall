# Use Host-Managed Trusted Plugin Artifact Roots

Maya Stall will keep Fresh Run workspaces clean and per-run, but may also copy
declared Plugin Artifacts to an operator-configured stable root on the Maya
Host. The stable root lives in host config as `trustedPluginArtifactsRoot`, not
Repo Run Config, and Scenario scripts receive it through
`MAYA_STALL_TRUSTED_PLUGIN_ARTIFACTS_ROOT`.

This lets operators add one narrow Maya trusted plug-in location while
consuming repos still declare the exact Plugin Artifacts they want staged.
Maya Stall copies only `pluginArtifacts` payload entries to that root, removes
each declared destination before upload to avoid stale directory contents, and
continues to stage the full Run Payload in the clean per-run workspace.

Do not trust `workRoot`, `workRoot/runs`, any ancestor of `workRoot/runs`, or
every run workspace. That would weaken Maya's location-based secure plug-in
loading and make arbitrary staged run content trusted. Scenario authors should
load plug-ins from the trusted root when the env var is present, then fall back
to the per-run payload path for fake or local runs.

# Split Repo Run Config From Host Credentials

Maya Stall will read non-secret Repo Run Config from `.maya-stall.yaml` or `maya-stall.yaml` in consuming repositories and keep Host Credentials and Host Pool details in user config, CI variables, or runner credentials. This is stricter than Crabbox static SSH config because Maya Hosts are private studio infrastructure; SSH keys, host names, Windows credentials, broker endpoints, and license-related secrets should stay out of plugin repositories.

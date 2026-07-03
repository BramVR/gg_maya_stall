# Sync Only Declared Payload Paths

Maya Stall v1 will sync only paths declared by the selected Scenario rather than the whole Git working set. Crabbox syncs tracked and nonignored files for general remote command execution, but Maya Stall's typed Run Payload can avoid transferring unrelated source, private files, and caches while still including ignored build outputs such as Plugin Artifacts when explicitly configured.

# Store Run State Separately From Evidence Bundles

Maya Stall will keep internal run state under a hidden repo-local directory and write user-facing Evidence Bundles to an obvious artifacts directory by default. This follows Crabbox's split between transient local state and proof bundles while making CI artifact upload straightforward.

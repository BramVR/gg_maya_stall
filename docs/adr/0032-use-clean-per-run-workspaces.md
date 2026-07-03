# Use Clean Per-Run Workspaces

Maya Stall Fresh Runs will stage payloads into a new remote run workspace instead of reusing or overlaying an existing directory. Crabbox can overlay sync into reused leases, but Maya Stall targets persistent owned hosts where stale Maya outputs are risky and declared payloads should be small enough that clean per-run staging is cheap.

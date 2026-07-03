# Manage Run Retention On Owned Hosts

Maya Stall v1 will include basic retention cleanup for remote run workspaces and local run state, with optional Evidence Store retention. Crabbox static SSH hosts leave cleanup to the user, but Maya Stall uses persistent owned Maya Hosts where run directories and evidence can pile up; Kept Sessions still need an explicit TTL so a host is not locked forever.

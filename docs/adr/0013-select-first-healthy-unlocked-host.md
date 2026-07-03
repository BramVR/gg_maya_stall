# Select First Healthy Unlocked Host

Maya Stall Target Profiles may reference a Host Pool. For v1, Maya Stall will choose the first healthy unlocked Maya Host, allow an explicit host pin for debugging, and wait or fail fast when every host is locked. This borrows Crabbox's ready-pool idea but keeps the owned-host model simple: no provisioning, no cost accounting, and host draining only for infrastructure or session health failures.

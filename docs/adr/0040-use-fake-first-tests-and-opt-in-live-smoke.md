# Use Fake-First Tests And Opt-In Live Smoke

Maya Stall will follow Crabbox's testing pattern: default tests use fake hosts, fake Session Brokers, fake filesystems, and fake publishers, while live Maya Host smoke tests are opt-in through explicit environment/configuration. The normal test suite must not require Autodesk Maya, private hosts, or secrets.

Fake-first tests are not real-product proof. When the checked-in proof policy marks a PR as live Maya required, CI must run the opt-in live smoke against configured host credentials and fail closed if the smoke is skipped, missing, or fake-only.

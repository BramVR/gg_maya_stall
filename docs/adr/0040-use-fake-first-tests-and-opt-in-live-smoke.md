# Use Fake-First Tests And Opt-In Live Smoke

Maya Stall will follow Crabbox's testing pattern: default tests use fake hosts, fake Session Brokers, fake filesystems, and fake publishers, while live Maya Host smoke tests are opt-in through explicit environment/configuration. The normal test suite must not require Autodesk Maya, private hosts, or secrets.

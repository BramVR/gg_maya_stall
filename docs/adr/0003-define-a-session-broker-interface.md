# Define A Session Broker Interface

Maya Stall will define a small Session Broker role and use the Maya Session Daemon as the only v1 implementation. This keeps the tool honest about the current dependency on `gg_mayasessiond` while preventing the CLI, config, and run model from hard-coding daemon-specific language everywhere.

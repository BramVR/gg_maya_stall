# Use Layered Host Health Checks

Maya Stall will model Host Health as layered checks instead of a single ping: local config, SSH, writable workspace, Session Broker reachability, Maya executable/version/license readiness, Visual Evidence capture, Host Lock state, and Scenario inputs. This mirrors Crabbox's doctor and desktop-doctor style and should produce clear failure messages at the layer that actually failed.

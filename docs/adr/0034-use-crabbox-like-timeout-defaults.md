# Use Crabbox-Like Timeout Defaults

Maya Stall will use Crabbox-like defaults where they map: kept-session TTL around 90 minutes, idle timeout around 30 minutes, and screenshot settle around 2 seconds. Recording timing defaults are deferred with recording support until the Session Broker exposes real recording capture. Maya-specific defaults such as Maya launch and Scenario timeout will be explicit because Crabbox has no equivalent application startup contract.

# Use Crabbox-Like Timeout Defaults

Maya Stall will use Crabbox-like defaults where they map: kept-session TTL of 90 minutes, idle timeout around 30 minutes, screenshot settle around 2 seconds, and desktop recording at 10 seconds and 15 fps. `run --keep-ttl <duration>` may override the kept-session TTL for one run. Maya-specific defaults such as Maya launch and Scenario timeout will be explicit because Crabbox has no equivalent application startup contract.

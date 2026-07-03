# Serialize Fresh Runs Per Maya Host

Maya Stall will allow only one active Fresh Run per Maya Host, enforced by a Host Lock with queue, timeout, or fail-fast behavior. Real Maya UI sessions share one Windows desktop and can corrupt each other if run in parallel, so concurrency belongs across hosts rather than inside one host.

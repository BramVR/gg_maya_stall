# Test V1 Machinery With Fakes

Maya Stall v1 should cover config, host selection and locking, payload planning, broker protocol adaptation, run lifecycle, Stop Policy, evidence layout, publish rendering, validators, doctor, init, and schema examples with default fake tests. Real Maya Host coverage remains opt-in live smoke, matching Crabbox's separation between deterministic machinery tests and environment-dependent provider tests.

For PR closeout, deterministic fake coverage and live provider coverage are separate gates. Live-touching changes need a Proof Manifest with `live_maya_required=true` and a passing live smoke; local fake tests alone leave the product proof incomplete.

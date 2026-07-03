# Consuming Repos Own Domain Correctness

Maya Stall will treat the consuming repo's Maya Scripts and Expected Outputs as the primary source of pass/fail truth, while providing reusable Validators for common comparisons. This keeps plugin-specific behavior out of Maya Stall core without forcing every repo to rewrite basic output checks.

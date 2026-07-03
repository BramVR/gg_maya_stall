# Use A Typed Maya Run Payload

Maya Stall will model the Run Payload as Maya-specific inputs such as Plugin Artifacts, Maya Scripts, scenes, and Expected Outputs instead of exposing only a generic synced folder plus command. The typed contract reduces per-repo convention drift and keeps the tool focused on real Maya UI end-to-end testing rather than becoming a hidden general CI runner.

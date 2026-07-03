# Stage Plugin Artifacts But Let Scenarios Load Them

Maya Stall will stage Plugin Artifacts into a predictable run workspace, but Scenario Maya Scripts will load the plugin and assert load success inside Maya. Crabbox only syncs files and runs commands; Maya Stall adds Maya-aware staging while keeping plugin-specific loading behavior in the consuming repo.

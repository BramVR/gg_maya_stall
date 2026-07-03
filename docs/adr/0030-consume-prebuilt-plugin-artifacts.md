# Consume Prebuilt Plugin Artifacts

Maya Stall v1 will consume prebuilt Plugin Artifacts rather than building them. Crabbox can run arbitrary build commands, but Maya Stall's typed Run Payload should stay focused on staging artifacts and running Maya Scenarios; consuming repo CI or local build steps own compilation and packaging before Maya Stall starts.

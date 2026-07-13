package cli

import "time"

// Defaults adapted from openclaw/crabbox visual evidence behavior.
const (
	defaultRecordingDuration = 10 * time.Second
	defaultRecordingFPS      = 15
	preRunProbeLayerTimeout  = 10 * time.Second
)

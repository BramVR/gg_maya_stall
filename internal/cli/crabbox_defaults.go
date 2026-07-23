package cli

import "time"

// Defaults adapted from openclaw/crabbox visual evidence behavior.
const (
	defaultKeepTTL           = 90 * time.Minute
	defaultRecordingDuration = 10 * time.Second
	defaultRecordingFPS      = 15
	preRunProbeLayerTimeout  = 10 * time.Second
)

package cli

import (
	"path/filepath"
	"sort"
	"strings"
)

const (
	evidenceBundleFileName           = "evidence.json"
	evidenceManifestFileName         = "manifest.json"
	evidenceEventsFileName           = "events.jsonl"
	evidenceLogPath                  = "logs/session.log"
	evidenceScenarioResultFileName   = "scenario-result.json"
	evidencePublishedManifestName    = "artifact-manifest.json"
	evidenceReviewCommentName        = "review-comment.md"
	evidenceScreenshotsDir           = "screenshots"
	evidenceRecordingsDir            = "recordings"
	evidenceDefaultScreenshotName    = "screenshot.png"
	evidenceDefaultRecordingName     = "recording.mp4"
	evidenceStandaloneResultName     = "scenario-result.json"
	evidenceStandaloneScenarioPrefix = "manual-"
)

type evidenceArtifact struct {
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	MediaType string `json:"mediaType,omitempty"`
}

func buildEvidenceBundleCatalog(bundle evidenceBundle) []evidenceArtifact {
	var artifacts []evidenceArtifact
	add := func(label string, kind string, path string, mediaType string) {
		clean := cleanEvidenceArtifactPath(path)
		if clean == "" {
			return
		}
		if mediaType == "" {
			mediaType = mediaTypeForPath(clean)
		}
		artifacts = append(artifacts, evidenceArtifact{
			Label:     label,
			Kind:      kind,
			Path:      clean,
			MediaType: mediaType,
		})
	}
	add("metadata", "metadata", evidenceBundleFileName, "application/json")
	add("metadata", "metadata", bundle.Manifest, "application/json")
	add("metadata", "metadata", bundle.ScenarioResult, "application/json")
	add("logs", "events", bundle.Events, "application/x-ndjson")
	add("logs", "log", bundle.Log, "text/plain")
	for _, artifact := range bundle.VisualEvidence {
		add("Visual Evidence", artifact.Kind, artifact.Path, artifact.MediaType)
	}
	for _, output := range bundle.Outputs {
		add("outputs", "output", output.Path, output.MediaType)
	}
	return sortEvidenceArtifactCatalog(dedupeEvidenceArtifactCatalog(artifacts))
}

func evidenceBundleCatalog(bundle evidenceBundle) []evidenceArtifact {
	if len(bundle.Artifacts) == 0 {
		return buildEvidenceBundleCatalog(bundle)
	}
	return sortEvidenceArtifactCatalog(dedupeEvidenceArtifactCatalog(bundle.Artifacts))
}

func dedupeEvidenceArtifactCatalog(artifacts []evidenceArtifact) []evidenceArtifact {
	seen := make(map[string]bool)
	catalog := make([]evidenceArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		artifact.Path = cleanEvidenceArtifactPath(artifact.Path)
		if artifact.Path == "" || seen[artifact.Path] {
			continue
		}
		if artifact.MediaType == "" {
			artifact.MediaType = mediaTypeForPath(artifact.Path)
		}
		seen[artifact.Path] = true
		catalog = append(catalog, artifact)
	}
	return catalog
}

func sortEvidenceArtifactCatalog(artifacts []evidenceArtifact) []evidenceArtifact {
	sort.SliceStable(artifacts, func(i, j int) bool {
		if artifacts[i].Label != artifacts[j].Label {
			return artifacts[i].Label < artifacts[j].Label
		}
		return artifacts[i].Path < artifacts[j].Path
	})
	return artifacts
}

func cleanEvidenceArtifactPath(path string) string {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}
	return clean
}

func isReservedEvidenceArtifactPath(path string) bool {
	slashed := strings.ToLower(filepath.ToSlash(path))
	for _, reserved := range []string{
		evidenceBundleFileName,
		evidenceManifestFileName,
		evidenceEventsFileName,
		evidenceScenarioResultFileName,
		evidencePublishedManifestName,
		evidenceReviewCommentName,
		"logs",
		evidenceScreenshotsDir,
		evidenceRecordingsDir,
	} {
		if slashed == reserved || strings.HasPrefix(slashed, reserved+"/") {
			return true
		}
	}
	return false
}

func mediaTypeForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "application/json"
	case ".txt", ".log":
		return "text/plain"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".mp4":
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}

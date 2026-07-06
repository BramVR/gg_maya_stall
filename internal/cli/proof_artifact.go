package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	liveProofArtifactEnabledEnv     = "MAYA_STALL_LIVE_PROOF_ARTIFACT_ENABLED"
	liveProofArtifactDirEnv         = "MAYA_STALL_LIVE_PROOF_ARTIFACT_DIR"
	liveProofRetentionDaysEnv       = "MAYA_STALL_LIVE_PROOF_RETENTION_DAYS"
	liveProofPublicHostAliasEnv     = "MAYA_STALL_LIVE_PROOF_PUBLIC_HOST_ALIAS"
	liveProofMediaReviewedEnv       = "MAYA_STALL_LIVE_PROOF_MEDIA_REVIEWED"
	liveProofArtifactManifestName   = "proof-artifact-manifest.json"
	liveProofEvidenceMetadataName   = "evidence-metadata.json"
	liveProofMediaReviewName        = "media-review.json"
	defaultLiveProofRetentionDays   = 3
	maximumLiveProofRetentionDays   = 14
	defaultLiveProofArtifactDirName = "artifacts/proof/live-visual-evidence"
)

type liveVisualEvidenceProofArtifactOptions struct {
	Enabled         bool
	Destination     string
	RetentionDays   int
	PublicHostAlias string
	MediaReviewed   bool
}

type liveVisualEvidenceProofArtifactManifest struct {
	RunID             string                                `json:"runId"`
	Scenario          string                                `json:"scenario"`
	TargetProfile     string                                `json:"targetProfile"`
	SelectedHostAlias string                                `json:"selectedHostAlias"`
	RetentionDays     int                                   `json:"retentionDays"`
	GeneratedAt       string                                `json:"generatedAt"`
	Artifacts         []liveVisualEvidenceProofArtifactFile `json:"artifacts"`
}

type liveVisualEvidenceProofArtifactFile struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	MediaType string `json:"mediaType"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
}

type liveVisualEvidenceProofMetadata struct {
	RunID          string                   `json:"runId"`
	Scenario       string                   `json:"scenario"`
	Status         string                   `json:"status"`
	TargetProfile  string                   `json:"targetProfile"`
	HostAlias      string                   `json:"hostAlias"`
	Runtime        runtimeMetadata          `json:"runtime"`
	VisualEvidence []visualEvidenceArtifact `json:"visualEvidence"`
}

type liveVisualEvidenceMediaReview struct {
	Reviewed bool     `json:"reviewed"`
	Scope    string   `json:"scope"`
	Paths    []string `json:"paths"`
}

func liveVisualEvidenceProofArtifactOptionsFromEnv(lookup func(string) (string, bool)) (liveVisualEvidenceProofArtifactOptions, error) {
	options := liveVisualEvidenceProofArtifactOptions{
		RetentionDays: defaultLiveProofRetentionDays,
	}
	enabled, ok := lookup(liveProofArtifactEnabledEnv)
	if !ok || strings.TrimSpace(enabled) == "" {
		return options, nil
	}
	parsed, err := parseBoolConfig(enabled)
	if err != nil {
		return liveVisualEvidenceProofArtifactOptions{}, fmt.Errorf("%s: %w", liveProofArtifactEnabledEnv, err)
	}
	options.Enabled = parsed
	if !options.Enabled {
		return options, nil
	}
	if destination, ok := lookup(liveProofArtifactDirEnv); ok && strings.TrimSpace(destination) != "" {
		options.Destination = destination
	} else {
		options.Destination = defaultLiveProofArtifactDirName
	}
	if retention, ok := lookup(liveProofRetentionDaysEnv); ok && strings.TrimSpace(retention) != "" {
		days, err := strconv.Atoi(strings.TrimSpace(retention))
		if err != nil || days < 1 || days > maximumLiveProofRetentionDays {
			return liveVisualEvidenceProofArtifactOptions{}, fmt.Errorf("%s must be 1-%d days", liveProofRetentionDaysEnv, maximumLiveProofRetentionDays)
		}
		options.RetentionDays = days
	}
	if alias, ok := lookup(liveProofPublicHostAliasEnv); ok {
		options.PublicHostAlias = strings.TrimSpace(alias)
	}
	if reviewed, ok := lookup(liveProofMediaReviewedEnv); ok && strings.TrimSpace(reviewed) != "" {
		parsed, err := parseBoolConfig(reviewed)
		if err != nil {
			return liveVisualEvidenceProofArtifactOptions{}, fmt.Errorf("%s: %w", liveProofMediaReviewedEnv, err)
		}
		options.MediaReviewed = parsed
	}
	return options, nil
}

func parseBoolConfig(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("expected true/false")
	}
}

func publishLiveVisualEvidenceProofArtifact(evidenceDir string, options liveVisualEvidenceProofArtifactOptions) (string, error) {
	if !options.Enabled {
		return "", nil
	}
	if options.RetentionDays == 0 {
		options.RetentionDays = defaultLiveProofRetentionDays
	}
	if options.Destination == "" {
		options.Destination = defaultLiveProofArtifactDirName
	}
	bundle, err := readEvidenceBundleFile(evidenceDir)
	if err != nil {
		return "", err
	}
	if err := requireLiveRuntime(bundle.Runtime); err != nil {
		return "", err
	}
	screenshot, recording, err := liveDesktopVisualArtifacts(bundle)
	if err != nil {
		return "", err
	}
	if !options.MediaReviewed {
		return "", fmt.Errorf("live proof artifact requires reviewed desktop media; set %s=true only for a controlled public-proof desktop", liveProofMediaReviewedEnv)
	}
	hostAlias, err := publicProofHostAlias(options.PublicHostAlias)
	if err != nil {
		return "", err
	}

	destination := filepath.Clean(options.Destination)
	overlap, err := pathsOverlap(evidenceDir, destination)
	if err != nil {
		return "", err
	}
	if overlap {
		return "", fmt.Errorf("live proof artifact destination must not overlap the Evidence Bundle")
	}
	info, err := os.Lstat(destination)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("live proof artifact destination must not be a symlink")
	}
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	if err := os.RemoveAll(destination); err != nil {
		return "", err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return "", err
	}

	metadata := liveVisualEvidenceProofMetadata{
		RunID:         bundle.RunID,
		Scenario:      bundle.Scenario,
		Status:        bundle.Status,
		TargetProfile: bundle.TargetProfile,
		HostAlias:     hostAlias,
		Runtime:       bundle.Runtime,
		VisualEvidence: []visualEvidenceArtifact{
			publicVisualEvidenceArtifact(screenshot, hostAlias),
			publicVisualEvidenceArtifact(recording, hostAlias),
		},
	}
	metadataBytes, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return "", err
	}
	metadataBytes = append(metadataBytes, '\n')
	if err := rejectConfidentialProofText(liveProofEvidenceMetadataName, string(metadataBytes)); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(destination, liveProofEvidenceMetadataName), metadataBytes, 0o644); err != nil {
		return "", err
	}
	review := liveVisualEvidenceMediaReview{
		Reviewed: true,
		Scope:    "controlled-public-proof-desktop",
		Paths:    []string{screenshot.Path, recording.Path},
	}
	reviewBytes, err := json.MarshalIndent(review, "", "  ")
	if err != nil {
		return "", err
	}
	reviewBytes = append(reviewBytes, '\n')
	if err := rejectConfidentialProofText(liveProofMediaReviewName, string(reviewBytes)); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(destination, liveProofMediaReviewName), reviewBytes, 0o644); err != nil {
		return "", err
	}

	files := []struct {
		source    string
		relative  string
		kind      string
		mediaType string
	}{
		{source: filepath.Join(destination, liveProofEvidenceMetadataName), relative: liveProofEvidenceMetadataName, kind: "metadata", mediaType: "application/json"},
		{source: filepath.Join(destination, liveProofMediaReviewName), relative: liveProofMediaReviewName, kind: "media-review", mediaType: "application/json"},
		{source: filepath.Join(evidenceDir, filepath.FromSlash(screenshot.Path)), relative: screenshot.Path, kind: "screenshot", mediaType: screenshot.MediaType},
		{source: filepath.Join(evidenceDir, filepath.FromSlash(recording.Path)), relative: recording.Path, kind: "recording", mediaType: recording.MediaType},
	}
	var artifacts []liveVisualEvidenceProofArtifactFile
	for _, file := range files {
		clean, err := cleanPublishedRelativePath(file.relative)
		if err != nil {
			return "", err
		}
		target := filepath.Join(destination, clean)
		if file.source != target {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			if err := copyFile(file.source, target); err != nil {
				return "", err
			}
		}
		entry, err := proofArtifactFileEntry(destination, filepath.ToSlash(clean), file.kind, file.mediaType)
		if err != nil {
			return "", err
		}
		artifacts = append(artifacts, entry)
	}
	sort.SliceStable(artifacts, func(i, j int) bool {
		return artifacts[i].Path < artifacts[j].Path
	})
	manifest := liveVisualEvidenceProofArtifactManifest{
		RunID:             bundle.RunID,
		Scenario:          bundle.Scenario,
		TargetProfile:     bundle.TargetProfile,
		SelectedHostAlias: hostAlias,
		RetentionDays:     options.RetentionDays,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		Artifacts:         artifacts,
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := rejectConfidentialProofText(liveProofArtifactManifestName, string(manifestBytes)); err != nil {
		return "", err
	}
	manifestPath := filepath.Join(destination, liveProofArtifactManifestName)
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		return "", err
	}
	return destination, nil
}

func requireLiveRuntime(runtime runtimeMetadata) error {
	if runtime.Profile != "ssh-sessiond" || runtime.HostAdapter != "ssh" || runtime.BrokerAdapter != "gg-mayasessiond" || !runtime.LiveProofEligible {
		return fmt.Errorf("visual evidence proof runtime = %+v, want live-proof-eligible ssh-sessiond", runtime)
	}
	return nil
}

func liveDesktopVisualArtifacts(bundle evidenceBundle) (visualEvidenceArtifact, visualEvidenceArtifact, error) {
	var screenshot visualEvidenceArtifact
	var recording visualEvidenceArtifact
	for _, artifact := range bundle.VisualEvidence {
		switch {
		case artifact.Kind == "screenshot" && artifact.MediaType == "image/png" && artifact.Path == "screenshots/desktop-screenshot.png":
			screenshot = artifact
		case artifact.Kind == "recording" && artifact.MediaType == "video/mp4" && artifact.Path == "recordings/desktop-recording.mp4":
			recording = artifact
		}
	}
	if screenshot.Path == "" || recording.Path == "" {
		return visualEvidenceArtifact{}, visualEvidenceArtifact{}, fmt.Errorf("live Visual Evidence proof requires desktop screenshot and desktop recording artifacts, got %+v", bundle.VisualEvidence)
	}
	return screenshot, recording, nil
}

func proofArtifactFileEntry(root string, relative string, kind string, mediaType string) (liveVisualEvidenceProofArtifactFile, error) {
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return liveVisualEvidenceProofArtifactFile{}, err
	}
	sum := sha256.Sum256(content)
	return liveVisualEvidenceProofArtifactFile{
		Path:      filepath.ToSlash(relative),
		Kind:      kind,
		MediaType: mediaType,
		Bytes:     int64(len(content)),
		SHA256:    hex.EncodeToString(sum[:]),
	}, nil
}

func publicVisualEvidenceArtifact(artifact visualEvidenceArtifact, hostAlias string) visualEvidenceArtifact {
	artifact.Host = hostAlias
	return artifact
}

func publicProofHostAlias(configured string) (string, error) {
	alias := strings.TrimSpace(configured)
	if alias == "" {
		return "", fmt.Errorf("live proof artifact needs a public selected host alias; set %s", liveProofPublicHostAliasEnv)
	}
	if !publicProofAliasPattern.MatchString(alias) || looksLikePrivateHostAlias(alias) {
		return "", fmt.Errorf("live proof artifact host alias %q is not public-safe; set %s", alias, liveProofPublicHostAliasEnv)
	}
	return alias, nil
}

var publicProofAliasPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func looksLikePrivateHostAlias(alias string) bool {
	lower := strings.ToLower(alias)
	return strings.HasPrefix(lower, "desktop-") ||
		strings.Contains(lower, ".") ||
		strings.Contains(lower, "\\") ||
		strings.Contains(lower, "@")
}

func rejectConfidentialProofText(path string, content string) error {
	if match := confidentialProofTextPattern.FindString(content); match != "" {
		return fmt.Errorf("live proof artifact %s contains confidential-looking text", path)
	}
	return nil
}

var confidentialProofTextPattern = regexp.MustCompile(`(?i)(BEGIN (OPENSSH|RSA|DSA|EC|PRIVATE)( PRIVATE)? KEY|ssh-rsa|ssh-ed25519|GITHUB_TOKEN|GH_TOKEN|GITLAB_TOKEN|MAYA_LICENSE|ADSKFLEX|LM_LICENSE_FILE|\.ssh/|C:\\Users\\[^\\\s]+|/Users/[^/\s]+|"?\b(password|token|secret)"?\s*[:=])`)

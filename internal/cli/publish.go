package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type evidencePublishOptions struct {
	Destination string
	BaseURL     string
	BundleDir   string
}

type publishedEvidence struct {
	RunID        string
	PublishedDir string
	ManifestPath string
	MarkdownPath string
	URL          string
}

type publishedArtifactManifest struct {
	RunID         string              `json:"runId"`
	Scenario      string              `json:"scenario"`
	Status        string              `json:"status"`
	TargetProfile string              `json:"targetProfile"`
	Host          string              `json:"host"`
	BaseURL       string              `json:"baseUrl"`
	Artifacts     []publishedArtifact `json:"artifacts"`
}

type publishedArtifact struct {
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	URL       string `json:"url"`
	MediaType string `json:"mediaType,omitempty"`
}

func parseEvidencePublishArgs(args []string) (evidencePublishOptions, error) {
	var options evidencePublishOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--destination":
			i++
			if i >= len(args) || args[i] == "" {
				return evidencePublishOptions{}, newUsageError("--destination needs a filesystem Evidence Store path")
			}
			options.Destination = args[i]
		case "--base-url":
			i++
			if i >= len(args) || args[i] == "" {
				return evidencePublishOptions{}, newUsageError("--base-url needs a URL")
			}
			options.BaseURL = args[i]
		default:
			if strings.HasPrefix(arg, "-") {
				return evidencePublishOptions{}, newUsageError("unknown evidence publish option %q", arg)
			}
			if options.BundleDir != "" {
				return evidencePublishOptions{}, newUsageError("evidence publish needs one Evidence Bundle directory")
			}
			options.BundleDir = arg
		}
	}
	if options.Destination == "" {
		return evidencePublishOptions{}, newUsageError("evidence publish needs --destination")
	}
	if options.BaseURL == "" {
		return evidencePublishOptions{}, newUsageError("evidence publish needs --base-url")
	}
	if _, err := parseBaseURL(options.BaseURL); err != nil {
		return evidencePublishOptions{}, err
	}
	if options.BundleDir == "" {
		return evidencePublishOptions{}, newUsageError("evidence publish needs an Evidence Bundle directory")
	}
	return options, nil
}

func publishEvidenceBundle(repoDir string, options evidencePublishOptions) (publishedEvidence, error) {
	bundleDir := resolveFromRepo(repoDir, options.BundleDir)
	destinationRoot := resolveFromRepo(repoDir, options.Destination)
	bundle, err := readEvidenceBundleFile(bundleDir)
	if err != nil {
		return publishedEvidence{}, err
	}
	if bundle.RunID == "" {
		bundle.RunID = filepath.Base(bundleDir)
	}
	if err := validateRunID(bundle.RunID); err != nil {
		return publishedEvidence{}, err
	}

	publishedDir := filepath.Join(destinationRoot, bundle.RunID)
	overlap, err := pathsOverlap(bundleDir, publishedDir)
	if err != nil {
		return publishedEvidence{}, err
	}
	if overlap {
		return publishedEvidence{}, fmt.Errorf("Evidence Store destination must not overlap the source Evidence Bundle")
	}
	baseURL := strings.TrimRight(options.BaseURL, "/")
	if err := replacePublishedDir(bundleDir, publishedDir, func(stagingDir string) error {
		manifest, err := buildPublishedArtifactManifest(stagingDir, bundle, baseURL)
		if err != nil {
			return err
		}
		if err := writeJSONFile(filepath.Join(stagingDir, "artifact-manifest.json"), manifest); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(stagingDir, "review-comment.md"), []byte(renderReviewMarkdown(bundle, manifest)), 0o644)
	}); err != nil {
		return publishedEvidence{}, err
	}
	publishedURL, err := artifactURL(baseURL, bundle.RunID, "review-comment.md")
	if err != nil {
		return publishedEvidence{}, err
	}
	return publishedEvidence{
		RunID:        bundle.RunID,
		PublishedDir: publishedDir,
		ManifestPath: filepath.Join(publishedDir, "artifact-manifest.json"),
		MarkdownPath: filepath.Join(publishedDir, "review-comment.md"),
		URL:          publishedURL,
	}, nil
}

func readEvidenceBundleFile(bundleDir string) (evidenceBundle, error) {
	content, err := os.ReadFile(filepath.Join(bundleDir, "evidence.json"))
	if err != nil {
		return evidenceBundle{}, err
	}
	var bundle evidenceBundle
	if err := json.Unmarshal(content, &bundle); err != nil {
		return evidenceBundle{}, fmt.Errorf("parse Evidence Bundle: %w", err)
	}
	return bundle, nil
}

func replacePublishedDir(bundleDir string, publishedDir string, populate func(stagingDir string) error) error {
	info, err := os.Lstat(publishedDir)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("published Evidence Bundle destination %s must not be a symlink", publishedDir)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	publishedExists := err == nil
	parent := filepath.Dir(publishedDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	stagingDir, err := os.MkdirTemp(parent, "."+filepath.Base(publishedDir)+".tmp-")
	if err != nil {
		return err
	}
	if err := copyPath(bundleDir, stagingDir); err != nil {
		_ = os.RemoveAll(stagingDir)
		return fmt.Errorf("copy Evidence Bundle into staging directory: %w", err)
	}
	if err := populate(stagingDir); err != nil {
		_ = os.RemoveAll(stagingDir)
		return err
	}
	if !publishedExists {
		return os.Rename(stagingDir, publishedDir)
	}
	backupDir, err := os.MkdirTemp(parent, "."+filepath.Base(publishedDir)+".backup-")
	if err != nil {
		_ = os.RemoveAll(stagingDir)
		return err
	}
	if err := os.Remove(backupDir); err != nil {
		_ = os.RemoveAll(stagingDir)
		return err
	}
	if err := os.Rename(publishedDir, backupDir); err != nil {
		_ = os.RemoveAll(stagingDir)
		return err
	}
	if err := os.Rename(stagingDir, publishedDir); err != nil {
		_ = os.Rename(backupDir, publishedDir)
		_ = os.RemoveAll(stagingDir)
		return err
	}
	return os.RemoveAll(backupDir)
}

func buildPublishedArtifactManifest(publishedDir string, bundle evidenceBundle, baseURL string) (publishedArtifactManifest, error) {
	var artifacts []publishedArtifact
	add := func(label string, kind string, path string, mediaType string) error {
		if path == "" {
			return nil
		}
		clean, err := cleanPublishedRelativePath(path)
		if err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(publishedDir, clean)); err != nil {
			return err
		}
		artifactURL, err := artifactURL(baseURL, bundle.RunID, clean)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, publishedArtifact{
			Label:     label,
			Kind:      kind,
			Path:      filepath.ToSlash(clean),
			URL:       artifactURL,
			MediaType: mediaType,
		})
		return nil
	}
	for _, artifact := range bundle.VisualEvidence {
		if err := add("Visual Evidence", artifact.Kind, artifact.Path, artifact.MediaType); err != nil {
			return publishedArtifactManifest{}, err
		}
	}
	if err := add("logs", "log", bundle.Log, "text/plain"); err != nil {
		return publishedArtifactManifest{}, err
	}
	if err := add("logs", "events", bundle.Events, "application/x-ndjson"); err != nil {
		return publishedArtifactManifest{}, err
	}
	for _, path := range []string{"evidence.json", bundle.Manifest, bundle.ScenarioResult} {
		if err := add("metadata", "metadata", path, "application/json"); err != nil {
			return publishedArtifactManifest{}, err
		}
	}
	outputs := bundle.Outputs
	if len(outputs) == 0 {
		discovered, err := discoverPublishedOutputs(publishedDir)
		if err != nil {
			return publishedArtifactManifest{}, err
		}
		outputs = discovered
	}
	for _, output := range outputs {
		if err := add("outputs", "output", output.Path, output.MediaType); err != nil {
			return publishedArtifactManifest{}, err
		}
	}
	sort.SliceStable(artifacts, func(i, j int) bool {
		if artifacts[i].Label != artifacts[j].Label {
			return artifacts[i].Label < artifacts[j].Label
		}
		return artifacts[i].Path < artifacts[j].Path
	})
	return publishedArtifactManifest{
		RunID:         bundle.RunID,
		Scenario:      bundle.Scenario,
		Status:        bundle.Status,
		TargetProfile: bundle.TargetProfile,
		Host:          bundle.Host,
		BaseURL:       baseURL,
		Artifacts:     artifacts,
	}, nil
}

func discoverPublishedOutputs(publishedDir string) ([]outputArtifact, error) {
	root := filepath.Join(publishedDir, "outputs")
	var outputs []outputArtifact
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(publishedDir, path)
		if err != nil {
			return err
		}
		outputPath := filepath.ToSlash(relative)
		outputs = append(outputs, outputArtifact{Path: outputPath, MediaType: mediaTypeForPath(outputPath)})
		return nil
	})
	sort.Slice(outputs, func(i, j int) bool {
		return outputs[i].Path < outputs[j].Path
	})
	return outputs, err
}

func renderReviewMarkdown(bundle evidenceBundle, manifest publishedArtifactManifest) string {
	if manifest.TargetProfile == "" {
		manifest.TargetProfile = bundle.TargetProfile
	}
	if manifest.Host == "" {
		manifest.Host = bundle.Host
	}
	return renderReviewMarkdownFromManifest(manifest)
}

func renderReviewMarkdownFromManifest(manifest publishedArtifactManifest) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "<!-- maya-stall:evidence-comment -->\n")
	fmt.Fprintf(&builder, "## Maya Stall Evidence\n\n")
	fmt.Fprintf(&builder, "status: %s\n", markdownText(manifest.Status))
	fmt.Fprintf(&builder, "run: %s\n", markdownText(manifest.RunID))
	fmt.Fprintf(&builder, "scenario: %s\n", markdownText(manifest.Scenario))
	fmt.Fprintf(&builder, "targetProfile: %s\n", markdownText(manifest.TargetProfile))
	fmt.Fprintf(&builder, "host: %s\n\n", markdownText(manifest.Host))
	for _, artifact := range manifest.Artifacts {
		fmt.Fprintf(&builder, "- %s: [%s](<%s>)\n", markdownText(artifact.Label), markdownLinkText(artifact.Path), markdownLinkDestination(artifact.URL))
	}
	return builder.String()
}

func artifactURL(baseURL string, runID string, relativePath string) (string, error) {
	joined, err := url.JoinPath(baseURL, runID, filepath.ToSlash(relativePath))
	if err != nil {
		return "", err
	}
	return strings.NewReplacer(
		"[", "%5B",
		"]", "%5D",
		"(", "%28",
		")", "%29",
		"<", "%3C",
		">", "%3E",
	).Replace(joined), nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, newUsageError("invalid --base-url %q: %v", raw, err)
	}
	if parsed.Scheme == "" {
		return nil, newUsageError("--base-url must include a URL scheme")
	}
	return parsed, nil
}

func resolveFromRepo(repoDir string, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(repoDir, path)
}

func pathsOverlap(left string, right string) (bool, error) {
	leftPath, err := canonicalPathForOverlap(left)
	if err != nil {
		return false, err
	}
	rightPath, err := canonicalPathForOverlap(right)
	if err != nil {
		return false, err
	}
	return pathContainsOrSame(leftPath, rightPath) || pathContainsOrSame(rightPath, leftPath), nil
}

func canonicalPathForOverlap(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(absolute)
	if parent == absolute {
		return filepath.Clean(absolute), nil
	}
	resolvedParent, err := canonicalPathForOverlap(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(absolute)), nil
}

func pathContainsOrSame(parent string, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func cleanPublishedRelativePath(path string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("published artifact path %q must be relative", path)
	}
	return clean, nil
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

func markdownText(value string) string {
	return strings.NewReplacer(
		"\\", "\\\\",
		"]", "\\]",
		"[", "\\[",
		")", "\\)",
		"(", "\\(",
		"\n", " ",
		"\r", " ",
	).Replace(value)
}

func markdownLinkText(value string) string {
	return strings.NewReplacer(
		"\\", "\\\\",
		"]", "\\]",
		"[", "\\[",
		")", "\\)",
		"(", "\\(",
		"\n", " ",
		"\r", " ",
	).Replace(value)
}

func markdownLinkDestination(value string) string {
	return strings.NewReplacer(
		">", "%3E",
		"\n", "%0A",
		"\r", "%0D",
		" ", "%20",
	).Replace(value)
}

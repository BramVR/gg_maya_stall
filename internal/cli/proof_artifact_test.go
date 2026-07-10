package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveProofArtifactOptionsAreDisabledByDefault(t *testing.T) {
	options, err := liveVisualEvidenceProofArtifactOptionsFromEnv(func(string) (string, bool) {
		return "", false
	})
	if err != nil {
		t.Fatalf("parse options: %v", err)
	}
	if options.Enabled {
		t.Fatalf("options enabled by default")
	}
}

func TestLiveProofArtifactOptionsParseRetentionAndDestination(t *testing.T) {
	env := map[string]string{
		liveProofArtifactEnabledEnv: "true",
		liveProofArtifactDirEnv:     "/tmp/maya-stall-proof",
		liveProofRetentionDaysEnv:   "5",
		liveProofPublicHostAliasEnv: "maya-live-proof-host",
		liveProofMediaReviewedEnv:   "true",
	}
	options, err := liveVisualEvidenceProofArtifactOptionsFromEnv(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("parse options: %v", err)
	}
	if !options.Enabled || options.Destination != env[liveProofArtifactDirEnv] || options.RetentionDays != 5 || options.PublicHostAlias != env[liveProofPublicHostAliasEnv] || !options.MediaReviewed {
		t.Fatalf("options = %+v", options)
	}
}

func TestLiveProofArtifactOptionsRejectInvalidRetention(t *testing.T) {
	env := map[string]string{
		liveProofArtifactEnabledEnv: "true",
		liveProofRetentionDaysEnv:   "30",
	}
	_, err := liveVisualEvidenceProofArtifactOptionsFromEnv(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err == nil || !strings.Contains(err.Error(), "1-14") {
		t.Fatalf("retention error = %v, want range failure", err)
	}
}

func TestPublishLiveProofArtifactWritesManifestHashesAndSelectedContent(t *testing.T) {
	evidenceDir := writeLiveVisualEvidenceProofBundle(t,
		runtimeMetadata{Profile: "ssh-sessiond", HostAdapter: "ssh", BrokerAdapter: "gg-mayasessiond", LiveProofEligible: true},
		[]visualEvidenceArtifact{
			{Kind: "screenshot", Path: "screenshots/desktop-screenshot.png", MediaType: "image/png", Origin: visualEvidenceOriginBrokerCapture, SHA256: sha256HexOfBytes(pngHeaderBytes())},
			{Kind: "recording", Path: "recordings/desktop-recording.mp4", MediaType: "video/mp4", Origin: visualEvidenceOriginBrokerCapture, SHA256: sha256HexOfBytes(mp4HeaderBytes())},
			{Kind: "screenshot", Path: "screenshots/viewport.jpg", MediaType: "image/jpeg", Origin: visualEvidenceOriginBrokerCapture, SHA256: sha256HexOfBytes(jpegHeaderBytes())},
		},
		map[string][]byte{
			"screenshots/desktop-screenshot.png": pngHeaderBytes(),
			"recordings/desktop-recording.mp4":   mp4HeaderBytes(),
			"screenshots/viewport.jpg":           jpegHeaderBytes(),
		},
	)
	destination := filepath.Join(t.TempDir(), "proof")

	published, err := publishLiveVisualEvidenceProofArtifact(evidenceDir, liveVisualEvidenceProofArtifactOptions{
		Enabled:         true,
		Destination:     destination,
		RetentionDays:   2,
		PublicHostAlias: "maya-live-proof-host",
		MediaReviewed:   true,
	})
	if err != nil {
		t.Fatalf("publish proof artifact: %v", err)
	}
	if published != destination {
		t.Fatalf("published = %q, want %q", published, destination)
	}

	for _, relative := range []string{
		liveProofArtifactManifestName,
		liveProofEvidenceMetadataName,
		liveProofMediaReviewName,
		"screenshots/desktop-screenshot.png",
		"recordings/desktop-recording.mp4",
	} {
		if _, err := os.Stat(filepath.Join(destination, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("expected proof artifact %s: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(destination, "screenshots", "viewport.jpg")); !os.IsNotExist(err) {
		t.Fatalf("proof artifact included non-desktop viewport screenshot, stat err = %v", err)
	}

	var manifest liveVisualEvidenceProofArtifactManifest
	readJSONFile(t, filepath.Join(destination, liveProofArtifactManifestName), &manifest)
	if manifest.SelectedHostAlias != "maya-live-proof-host" || manifest.RetentionDays != 2 {
		t.Fatalf("manifest metadata = %+v", manifest)
	}
	wantPaths := map[string]string{
		liveProofEvidenceMetadataName:        "application/json",
		liveProofMediaReviewName:             "application/json",
		"screenshots/desktop-screenshot.png": "image/png",
		"recordings/desktop-recording.mp4":   "video/mp4",
	}
	if len(manifest.Artifacts) != len(wantPaths) {
		t.Fatalf("manifest artifacts = %+v, want %d entries", manifest.Artifacts, len(wantPaths))
	}
	for _, artifact := range manifest.Artifacts {
		wantMedia, ok := wantPaths[artifact.Path]
		if !ok {
			t.Fatalf("unexpected artifact in manifest: %+v", artifact)
		}
		if artifact.MediaType != wantMedia || artifact.Bytes <= 0 || len(artifact.SHA256) != 64 {
			t.Fatalf("bad artifact entry: %+v", artifact)
		}
		content, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(artifact.Path)))
		if err != nil {
			t.Fatalf("read artifact %s: %v", artifact.Path, err)
		}
		sum := sha256.Sum256(content)
		if artifact.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("artifact %s sha = %s, want %s", artifact.Path, artifact.SHA256, hex.EncodeToString(sum[:]))
		}
	}
}

func TestPublishLiveProofArtifactRejectsMissingLiveArtifactPath(t *testing.T) {
	evidenceDir := writeLiveVisualEvidenceProofBundle(t,
		runtimeMetadata{Profile: "ssh-sessiond", HostAdapter: "ssh", BrokerAdapter: "gg-mayasessiond", LiveProofEligible: true},
		[]visualEvidenceArtifact{
			{Kind: "screenshot", Path: "screenshots/desktop-screenshot.png", MediaType: "image/png", Origin: visualEvidenceOriginBrokerCapture, SHA256: sha256HexOfBytes(pngHeaderBytes())},
			{Kind: "recording", Path: "recordings/desktop-recording.mp4", MediaType: "video/mp4", Origin: visualEvidenceOriginBrokerCapture, SHA256: sha256HexOfBytes(mp4HeaderBytes())},
		},
		map[string][]byte{
			"screenshots/desktop-screenshot.png": pngHeaderBytes(),
		},
	)
	_, err := publishLiveVisualEvidenceProofArtifact(evidenceDir, liveVisualEvidenceProofArtifactOptions{
		Enabled:         true,
		Destination:     filepath.Join(t.TempDir(), "proof"),
		PublicHostAlias: "maya-live-proof-host",
		MediaReviewed:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "desktop-recording.mp4") {
		t.Fatalf("missing artifact error = %v", err)
	}
}

func TestPublishLiveProofArtifactRejectsTraversalRecordingPath(t *testing.T) {
	evidenceDir := writeLiveVisualEvidenceProofBundle(t,
		runtimeMetadata{Profile: "ssh-sessiond", HostAdapter: "ssh", BrokerAdapter: "gg-mayasessiond", LiveProofEligible: true},
		[]visualEvidenceArtifact{
			{Kind: "screenshot", Path: "screenshots/desktop-screenshot.png", MediaType: "image/png"},
			{Kind: "recording", Path: "recordings/../logs/session.log", MediaType: "video/mp4"},
		},
		map[string][]byte{
			"screenshots/desktop-screenshot.png": pngHeaderBytes(),
		},
	)
	_, err := publishLiveVisualEvidenceProofArtifact(evidenceDir, liveVisualEvidenceProofArtifactOptions{
		Enabled:         true,
		Destination:     filepath.Join(t.TempDir(), "proof"),
		PublicHostAlias: "maya-live-proof-host",
		MediaReviewed:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "desktop recording artifacts") {
		t.Fatalf("traversal recording path error = %v", err)
	}
}

func TestPublishLiveProofArtifactRejectsConfidentialHostAlias(t *testing.T) {
	evidenceDir := writeLiveVisualEvidenceProofBundle(t,
		runtimeMetadata{Profile: "ssh-sessiond", HostAdapter: "ssh", BrokerAdapter: "gg-mayasessiond", LiveProofEligible: true},
		[]visualEvidenceArtifact{
			{Kind: "screenshot", Path: "screenshots/desktop-screenshot.png", MediaType: "image/png", Origin: visualEvidenceOriginBrokerCapture, SHA256: sha256HexOfBytes(pngHeaderBytes())},
			{Kind: "recording", Path: "recordings/desktop-recording.mp4", MediaType: "video/mp4", Origin: visualEvidenceOriginBrokerCapture, SHA256: sha256HexOfBytes(mp4HeaderBytes())},
		},
		map[string][]byte{
			"screenshots/desktop-screenshot.png": pngHeaderBytes(),
			"recordings/desktop-recording.mp4":   mp4HeaderBytes(),
		},
	)
	bundle := readEvidenceBundle(t, evidenceDir)
	bundle.Host = "desktop-private"
	if err := writeJSONFile(filepath.Join(evidenceDir, evidenceBundleFileName), bundle); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	_, err := publishLiveVisualEvidenceProofArtifact(evidenceDir, liveVisualEvidenceProofArtifactOptions{
		Enabled:       true,
		Destination:   filepath.Join(t.TempDir(), "proof"),
		MediaReviewed: true,
	})
	if err == nil || !strings.Contains(err.Error(), liveProofPublicHostAliasEnv) {
		t.Fatalf("confidential alias error = %v", err)
	}
}

func TestPublishLiveProofArtifactRequiresReviewedMedia(t *testing.T) {
	evidenceDir := writeLiveVisualEvidenceProofBundle(t,
		runtimeMetadata{Profile: "ssh-sessiond", HostAdapter: "ssh", BrokerAdapter: "gg-mayasessiond", LiveProofEligible: true},
		[]visualEvidenceArtifact{
			{Kind: "screenshot", Path: "screenshots/desktop-screenshot.png", MediaType: "image/png", Origin: visualEvidenceOriginBrokerCapture, SHA256: sha256HexOfBytes(pngHeaderBytes())},
			{Kind: "recording", Path: "recordings/desktop-recording.mp4", MediaType: "video/mp4", Origin: visualEvidenceOriginBrokerCapture, SHA256: sha256HexOfBytes(mp4HeaderBytes())},
		},
		map[string][]byte{
			"screenshots/desktop-screenshot.png": pngHeaderBytes(),
			"recordings/desktop-recording.mp4":   mp4HeaderBytes(),
		},
	)
	_, err := publishLiveVisualEvidenceProofArtifact(evidenceDir, liveVisualEvidenceProofArtifactOptions{
		Enabled:         true,
		Destination:     filepath.Join(t.TempDir(), "proof"),
		PublicHostAlias: "maya-live-proof-host",
	})
	if err == nil || !strings.Contains(err.Error(), liveProofMediaReviewedEnv) {
		t.Fatalf("media review error = %v", err)
	}
}

func TestPublishLiveProofArtifactRejectsNonBrokerProvenance(t *testing.T) {
	cases := []struct {
		name    string
		visual  []visualEvidenceArtifact
		wantErr string
	}{
		{
			name: "discovered origin",
			visual: []visualEvidenceArtifact{
				{Kind: "screenshot", Path: "screenshots/desktop-screenshot.png", MediaType: "image/png", Origin: visualEvidenceOriginDiscovered, SHA256: sha256HexOfBytes(pngHeaderBytes())},
				{Kind: "recording", Path: "recordings/desktop-recording.mp4", MediaType: "video/mp4", Origin: visualEvidenceOriginBrokerCapture, SHA256: sha256HexOfBytes(mp4HeaderBytes())},
			},
			wantErr: `origin "discovered"`,
		},
		{
			name: "fake broker origin",
			visual: []visualEvidenceArtifact{
				{Kind: "screenshot", Path: "screenshots/desktop-screenshot.png", MediaType: "image/png", Origin: visualEvidenceOriginFakeBrokerCapture, SHA256: sha256HexOfBytes(pngHeaderBytes())},
				{Kind: "recording", Path: "recordings/desktop-recording.mp4", MediaType: "video/mp4", Origin: visualEvidenceOriginBrokerCapture, SHA256: sha256HexOfBytes(mp4HeaderBytes())},
			},
			wantErr: `origin "fake-broker-capture"`,
		},
		{
			name: "missing origin",
			visual: []visualEvidenceArtifact{
				{Kind: "screenshot", Path: "screenshots/desktop-screenshot.png", MediaType: "image/png", SHA256: sha256HexOfBytes(pngHeaderBytes())},
				{Kind: "recording", Path: "recordings/desktop-recording.mp4", MediaType: "video/mp4", Origin: visualEvidenceOriginBrokerCapture, SHA256: sha256HexOfBytes(mp4HeaderBytes())},
			},
			wantErr: `origin "unknown"`,
		},
		{
			name: "mismatched provenance hash",
			visual: []visualEvidenceArtifact{
				{Kind: "screenshot", Path: "screenshots/desktop-screenshot.png", MediaType: "image/png", Origin: visualEvidenceOriginBrokerCapture, SHA256: sha256HexOfBytes([]byte("tampered"))},
				{Kind: "recording", Path: "recordings/desktop-recording.mp4", MediaType: "video/mp4", Origin: visualEvidenceOriginBrokerCapture, SHA256: sha256HexOfBytes(mp4HeaderBytes())},
			},
			wantErr: "does not match recorded provenance hash",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			evidenceDir := writeLiveVisualEvidenceProofBundle(t,
				runtimeMetadata{Profile: "ssh-sessiond", HostAdapter: "ssh", BrokerAdapter: "gg-mayasessiond", LiveProofEligible: true},
				tt.visual,
				map[string][]byte{
					"screenshots/desktop-screenshot.png": pngHeaderBytes(),
					"recordings/desktop-recording.mp4":   mp4HeaderBytes(),
				},
			)
			_, err := publishLiveVisualEvidenceProofArtifact(evidenceDir, liveVisualEvidenceProofArtifactOptions{
				Enabled:         true,
				Destination:     filepath.Join(t.TempDir(), "proof"),
				PublicHostAlias: "maya-live-proof-host",
				MediaReviewed:   true,
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("publish proof artifact error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestPublishLiveProofArtifactRejectsConfidentialText(t *testing.T) {
	if err := rejectConfidentialProofText("metadata", `{"ssh":"BEGIN OPENSSH PRIVATE KEY"}`); err == nil {
		t.Fatalf("confidential text accepted")
	}
	if err := rejectConfidentialProofText("metadata", `{"token":"abc"}`); err == nil {
		t.Fatalf("quoted JSON token field accepted")
	}
}

func readJSONFile(t *testing.T, path string, target any) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(content, target); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

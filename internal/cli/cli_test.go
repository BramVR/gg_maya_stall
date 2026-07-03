package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpAndVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--help"}, &stdout, &stderr, "", "test-version")
	if code != 0 {
		t.Fatalf("help exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "maya-stall") || !strings.Contains(stdout.String(), "init") {
		t.Fatalf("help output missing command surface:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"version"}, &stdout, &stderr, "", "test-version")
	if code != 0 {
		t.Fatalf("version exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "maya-stall test-version" {
		t.Fatalf("version output = %q", got)
	}
}

func TestInitWritesRepoOnlySmokeScenario(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := Run([]string{"init"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("init exit code = %d, want 0; stderr: %s", code, stderr.String())
	}

	configPath := filepath.Join(dir, ".maya-stall.yaml")
	contentBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	content := string(contentBytes)
	for _, want := range []string{
		"version: 1",
		"scenarios:",
		"smoke:",
		"mayaVersion:",
		"payload:",
		"evidence:",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated config missing %q:\n%s", want, content)
		}
	}
	for _, forbidden := range []string{
		"host",
		"Host",
		"hostname",
		"Host Pool",
		"Host Credentials",
		"ssh",
		"credential",
		"password",
		"private",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("generated config contains forbidden host/credential detail %q:\n%s", forbidden, content)
		}
	}
}

func TestDiscoverConfigRecognizesSupportedRepoFilenames(t *testing.T) {
	for _, name := range []string{".maya-stall.yaml", "maya-stall.yaml"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("version: 1\nscenarios: {}\n"), 0o644); err != nil {
				t.Fatalf("write config fixture: %v", err)
			}

			got, err := DiscoverConfig(dir)
			if err != nil {
				t.Fatalf("DiscoverConfig returned error: %v", err)
			}
			if got != path {
				t.Fatalf("DiscoverConfig = %q, want %q", got, path)
			}
		})
	}
}

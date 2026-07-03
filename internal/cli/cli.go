package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const defaultConfigName = ".maya-stall.yaml"

var configNames = []string{".maya-stall.yaml", "maya-stall.yaml"}

// Run executes maya-stall with process-style arguments and returns an exit code.
func Run(args []string, stdout io.Writer, stderr io.Writer, workDir string, version string) int {
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "maya-stall: get working directory: %v\n", err)
			return 1
		}
	}
	if version == "" {
		version = "dev"
	}

	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printHelp(stdout)
		return 0
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Fprintf(stdout, "maya-stall %s\n", version)
		return 0
	}

	switch args[0] {
	case "init":
		if err := writeInitialConfig(workDir); err != nil {
			fmt.Fprintf(stderr, "maya-stall init: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "wrote %s\n", filepath.Join(workDir, defaultConfigName))
		return 0
	default:
		fmt.Fprintf(stderr, "maya-stall: unknown command %q\n\n", args[0])
		printHelp(stderr)
		return 2
	}
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `maya-stall runs Autodesk Maya UI Scenarios from repo-owned config.

Usage:
  maya-stall [--help]
  maya-stall version
  maya-stall init

Commands:
  init      write a repo-only sample .maya-stall.yaml
  version   print the maya-stall version
`)
}

// DiscoverConfig finds the Repo Run Config file in dir.
func DiscoverConfig(dir string) (string, error) {
	for _, name := range configNames {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("no Maya Stall repo config found in %s", dir)
}

func writeInitialConfig(dir string) error {
	path := filepath.Join(dir, defaultConfigName)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, []byte(initialConfig), 0o644)
}

const initialConfig = `version: 1
scenarios:
  smoke:
    description: "Open a minimal Maya scene and produce visual evidence."
    mayaVersion: "2025"
    payload:
      scripts:
        - "maya/smoke.py"
      scenes: []
      pluginArtifacts: []
    expectedOutputs:
      files: []
      scenarioResult: "outputs/smoke-result.json"
    evidence:
      screenshots:
        enabled: true
      recording:
        enabled: false
`

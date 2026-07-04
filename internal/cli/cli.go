package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultConfigName = ".maya-stall.yaml"

var configNames = []string{".maya-stall.yaml", "maya-stall.yaml"}

// Run executes maya-stall with process-style arguments and returns an exit code.
func Run(args []string, stdout io.Writer, stderr io.Writer, workDir string, version string) int {
	return RunWithRuntime(args, stdout, stderr, workDir, version, defaultRunRuntime())
}

func RunWithRuntime(args []string, stdout io.Writer, stderr io.Writer, workDir string, version string, runtime runRuntime) int {
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
	case "doctor":
		options, err := parseDoctorArgs(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "maya-stall doctor: %v\n", err)
			return 2
		}
		report := runDoctor(workDir, options)
		for _, check := range report.Checks {
			fmt.Fprintf(stdout, "%s: %s - %s\n", check.Layer, check.Status, check.Detail)
			if check.Hint != "" {
				fmt.Fprintf(stdout, "hint: %s\n", check.Hint)
			}
		}
		if report.Healthy {
			return 0
		}
		return 1
	case "run":
		options, err := parseRunArgs(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "maya-stall run: %v\n", err)
			return 2
		}
		outcome, err := runScenario(workDir, options, runtime)
		if err != nil {
			var userErr *usageError
			if errors.As(err, &userErr) {
				fmt.Fprintf(stderr, "maya-stall run: %v\n", err)
				return 2
			}
			fmt.Fprintf(stderr, "maya-stall run: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "run: %s\n", outcome.RunID)
		fmt.Fprintf(stdout, "scenario: %s\n", outcome.Scenario)
		fmt.Fprintf(stdout, "targetProfile: %s\n", outcome.TargetProfile)
		fmt.Fprintf(stdout, "host: %s\n", outcome.Host)
		fmt.Fprintf(stdout, "status: %s\n", outcome.Result.Status)
		fmt.Fprintf(stdout, "state: %s\n", outcome.StateDir)
		fmt.Fprintf(stdout, "evidence: %s\n", outcome.EvidenceDir)
		if outcome.Result.Status == resultStatusPassed {
			return 0
		}
		return 1
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
  maya-stall doctor [--host-config <path>] [--target-profile <name>] [--host <id>] [--scenario <name>]
  maya-stall run [--host-config <path>] [--target-profile <name>] [--host <id>] [--host-lock-wait <duration>|--host-lock-fail-fast] <scenario>

Commands:
  doctor   check local config, Target Profile, and fake Host Health layers
  init      write a repo-only sample .maya-stall.yaml
  run       run a named Scenario with the fake runtime
  version   print the maya-stall version
`)
}

type runOptions struct {
	ScenarioName  string
	HostConfig    string
	TargetProfile string
	HostPin       string
	HostLockWait  time.Duration
}

func parseRunArgs(args []string) (runOptions, error) {
	options := runOptions{TargetProfile: "default"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--host-config":
			i++
			if i >= len(args) || args[i] == "" {
				return runOptions{}, newUsageError("--host-config needs a path")
			}
			options.HostConfig = args[i]
		case "--target-profile":
			i++
			if i >= len(args) || args[i] == "" {
				return runOptions{}, newUsageError("--target-profile needs a name")
			}
			options.TargetProfile = args[i]
		case "--host":
			i++
			if i >= len(args) || args[i] == "" {
				return runOptions{}, newUsageError("--host needs a Maya Host id")
			}
			options.HostPin = args[i]
		case "--host-lock-wait":
			i++
			if i >= len(args) || args[i] == "" {
				return runOptions{}, newUsageError("--host-lock-wait needs a duration")
			}
			duration, err := time.ParseDuration(args[i])
			if err != nil {
				return runOptions{}, newUsageError("invalid --host-lock-wait duration %q", args[i])
			}
			options.HostLockWait = duration
		case "--host-lock-fail-fast":
			options.HostLockWait = 0
		default:
			if strings.HasPrefix(arg, "-") {
				return runOptions{}, newUsageError("unknown run option %q", arg)
			}
			if options.ScenarioName != "" {
				return runOptions{}, newUsageError("expected one Scenario name")
			}
			options.ScenarioName = arg
		}
	}
	if options.ScenarioName == "" {
		return runOptions{}, newUsageError("expected Scenario name")
	}
	return options, nil
}

type doctorOptions struct {
	HostConfig    string
	TargetProfile string
	HostPin       string
	ScenarioName  string
}

func parseDoctorArgs(args []string) (doctorOptions, error) {
	options := doctorOptions{TargetProfile: "default"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--host-config":
			i++
			if i >= len(args) || args[i] == "" {
				return doctorOptions{}, newUsageError("--host-config needs a path")
			}
			options.HostConfig = args[i]
		case "--target-profile":
			i++
			if i >= len(args) || args[i] == "" {
				return doctorOptions{}, newUsageError("--target-profile needs a name")
			}
			options.TargetProfile = args[i]
		case "--host":
			i++
			if i >= len(args) || args[i] == "" {
				return doctorOptions{}, newUsageError("--host needs a Maya Host id")
			}
			options.HostPin = args[i]
		case "--scenario":
			i++
			if i >= len(args) || args[i] == "" {
				return doctorOptions{}, newUsageError("--scenario needs a name")
			}
			options.ScenarioName = args[i]
		default:
			return doctorOptions{}, newUsageError("unknown doctor option %q", arg)
		}
	}
	return options, nil
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

type usageError struct {
	message string
}

func (e *usageError) Error() string {
	return e.message
}

func newUsageError(format string, args ...any) error {
	return &usageError{message: fmt.Sprintf(format, args...)}
}

func defaultRunRuntime() runRuntime {
	return runRuntime{
		Host:   fakeHost{},
		Broker: fakeSessionBroker{Result: ScenarioResult{Status: resultStatusPassed, Summary: "fake Scenario completed"}},
		Now:    time.Now,
	}
}

const initialConfig = `version: 1
scenarios:
  smoke:
    description: "Open a minimal Maya scene and produce visual evidence."
    mayaVersion: "2025"
    payload:
      mayaScripts: []
      scenes: []
      pluginArtifacts: []
      expectedOutputs: []
      includePaths: []
    expectedOutputs:
      files: []
      scenarioResult: "outputs/smoke-result.json"
    validators:
      - type: scenarioResultStatus
        status: passed
      - type: visualEvidence
        required: true
    evidence:
      screenshots:
        enabled: true
      recording:
        enabled: false
`

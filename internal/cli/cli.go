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
		printHostHealthReport(stdout, report)
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
		printRunOutcome(stdout, outcome)
		if outcome.Result.Status == resultStatusPassed {
			return 0
		}
		return 1
	case "screenshot":
		options, err := parseVisualEvidenceArgs(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "maya-stall screenshot: %v\n", err)
			return 2
		}
		outcome, artifact, err := captureStandaloneVisualEvidence(workDir, options, runtime, "screenshot")
		if err != nil {
			var userErr *usageError
			if errors.As(err, &userErr) {
				fmt.Fprintf(stderr, "maya-stall screenshot: %v\n", err)
				return 2
			}
			fmt.Fprintf(stderr, "maya-stall screenshot: %v\n", err)
			return 1
		}
		printVisualEvidenceOutcome(stdout, outcome, artifact)
		return 0
	case "record":
		options, err := parseVisualEvidenceArgs(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "maya-stall record: %v\n", err)
			return 2
		}
		outcome, artifact, err := captureStandaloneVisualEvidence(workDir, options, runtime, "recording")
		if err != nil {
			var userErr *usageError
			if errors.As(err, &userErr) {
				fmt.Fprintf(stderr, "maya-stall record: %v\n", err)
				return 2
			}
			fmt.Fprintf(stderr, "maya-stall record: %v\n", err)
			return 1
		}
		printVisualEvidenceOutcome(stdout, outcome, artifact)
		return 0
	case "control":
		options, err := parseDesktopControlArgs(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "maya-stall control: %v\n", err)
			return 2
		}
		outcome, err := runDesktopControl(workDir, options, runtime)
		if err != nil {
			var userErr *usageError
			if errors.As(err, &userErr) {
				fmt.Fprintf(stderr, "maya-stall control: %v\n", err)
				return 2
			}
			fmt.Fprintf(stderr, "maya-stall control: %v\n", err)
			return 1
		}
		printDesktopControlOutcome(stdout, outcome)
		return 0
	case "evidence":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "maya-stall evidence: expected collect or publish\n")
			return 2
		}
		switch args[1] {
		case "collect":
			options, err := parseRunArgs(args[2:])
			if err != nil {
				fmt.Fprintf(stderr, "maya-stall evidence collect: %v\n", err)
				return 2
			}
			outcome, err := runScenario(workDir, options, runtime)
			if err != nil {
				var userErr *usageError
				if errors.As(err, &userErr) {
					fmt.Fprintf(stderr, "maya-stall evidence collect: %v\n", err)
					return 2
				}
				fmt.Fprintf(stderr, "maya-stall evidence collect: %v\n", err)
				return 1
			}
			printRunOutcome(stdout, outcome)
			if outcome.Result.Status == resultStatusPassed {
				return 0
			}
			return 1
		case "publish":
			options, err := parseEvidencePublishArgs(args[2:])
			if err != nil {
				fmt.Fprintf(stderr, "maya-stall evidence publish: %v\n", err)
				return 2
			}
			published, err := publishEvidenceBundle(workDir, options)
			if err != nil {
				var userErr *usageError
				if errors.As(err, &userErr) {
					fmt.Fprintf(stderr, "maya-stall evidence publish: %v\n", err)
					return 2
				}
				fmt.Fprintf(stderr, "maya-stall evidence publish: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "run: %s\n", published.RunID)
			fmt.Fprintf(stdout, "published: %s\n", published.PublishedDir)
			fmt.Fprintf(stdout, "artifactManifest: %s\n", published.ManifestPath)
			fmt.Fprintf(stdout, "reviewComment: %s\n", published.MarkdownPath)
			fmt.Fprintf(stdout, "url: %s\n", published.URL)
			return 0
		default:
			fmt.Fprintf(stderr, "maya-stall evidence: expected collect or publish\n")
			return 2
		}
	case "review-comment":
		options, err := parseReviewCommentArgs(args[1:], os.LookupEnv)
		if err != nil {
			fmt.Fprintf(stderr, "maya-stall review-comment: %v\n", err)
			return 2
		}
		result, markdownPath, err := postReviewComment(workDir, options, nil)
		if err != nil {
			var userErr *usageError
			if errors.As(err, &userErr) {
				fmt.Fprintf(stderr, "maya-stall review-comment: %v\n", err)
				return 2
			}
			fmt.Fprintf(stderr, "maya-stall review-comment: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "platform: %s\n", result.Platform)
		fmt.Fprintf(stdout, "operation: %s\n", result.Operation)
		switch options.Platform {
		case "github":
			fmt.Fprintf(stdout, "target: %s#%d\n", options.GitHub.Repo, options.GitHub.PullRequest)
		case "gitlab":
			fmt.Fprintf(stdout, "target: %s!%d\n", options.GitLab.Project, options.GitLab.MergeRequest)
		}
		if result.CommentID != "" {
			fmt.Fprintf(stdout, "comment: %s\n", result.CommentID)
		}
		fmt.Fprintf(stdout, "reviewComment: %s\n", markdownPath)
		return 0
	case "status":
		options, err := parseStatusArgs(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "maya-stall status: %v\n", err)
			return 2
		}
		if err := printStatus(workDir, options, stdout); err != nil {
			var userErr *usageError
			if errors.As(err, &userErr) {
				fmt.Fprintf(stderr, "maya-stall status: %v\n", err)
				return 2
			}
			fmt.Fprintf(stderr, "maya-stall status: %v\n", err)
			return 1
		}
		return 0
	case "attach":
		runID, err := parseRunIDArg("attach", args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "maya-stall attach: %v\n", err)
			return 2
		}
		if err := attachRun(workDir, runID, stdout); err != nil {
			var userErr *usageError
			if errors.As(err, &userErr) {
				fmt.Fprintf(stderr, "maya-stall attach: %v\n", err)
				return 2
			}
			fmt.Fprintf(stderr, "maya-stall attach: %v\n", err)
			return 1
		}
		return 0
	case "stop":
		runID, err := parseRunIDArg("stop", args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "maya-stall stop: %v\n", err)
			return 2
		}
		if err := stopRun(workDir, runID); err != nil {
			var userErr *usageError
			if errors.As(err, &userErr) {
				fmt.Fprintf(stderr, "maya-stall stop: %v\n", err)
				return 2
			}
			fmt.Fprintf(stderr, "maya-stall stop: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "stopped: %s\n", runID)
		return 0
	default:
		fmt.Fprintf(stderr, "maya-stall: unknown command %q\n\n", args[0])
		printHelp(stderr)
		return 2
	}
}

func printRunOutcome(stdout io.Writer, outcome runOutcome) {
	fmt.Fprintf(stdout, "run: %s\n", outcome.RunID)
	fmt.Fprintf(stdout, "scenario: %s\n", outcome.Scenario)
	fmt.Fprintf(stdout, "targetProfile: %s\n", outcome.TargetProfile)
	fmt.Fprintf(stdout, "host: %s\n", outcome.Host)
	fmt.Fprintf(stdout, "status: %s\n", outcome.Result.Status)
	fmt.Fprintf(stdout, "stopPolicy: %s\n", outcome.StopPolicy)
	fmt.Fprintf(stdout, "state: %s\n", outcome.StateDir)
	fmt.Fprintf(stdout, "evidence: %s\n", outcome.EvidenceDir)
	for _, validator := range outcome.Validators {
		if validator.Status != resultStatusPassed {
			fmt.Fprintf(stdout, "validator: %s %s - %s\n", validator.Type, validator.Status, validator.Message)
		}
	}
	for _, command := range outcome.FollowUpCommands {
		fmt.Fprintf(stdout, "next: %s\n", command)
	}
}

func printVisualEvidenceOutcome(stdout io.Writer, outcome runOutcome, artifact visualEvidenceArtifact) {
	printRunOutcome(stdout, outcome)
	fmt.Fprintf(stdout, "artifact: %s\n", artifact.Path)
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `maya-stall runs Autodesk Maya UI Scenarios from repo-owned config.

Usage:
  maya-stall [--help]
  maya-stall version
  maya-stall init
  maya-stall doctor [--host-config <path>] [--target-profile <name>] [--host <id>] [--scenario <name>]
  maya-stall run [--host-config <path>] [--target-profile <name>] [--host <id>] [--host-lock-wait <duration>|--host-lock-fail-fast] [--keep-on-failure|--stop-after <success|failure|always|never>] <scenario>
  maya-stall screenshot [--host-config <path>] [--target-profile <name>] [--host <id>]
  maya-stall record [--host-config <path>] [--target-profile <name>] [--host <id>]
  maya-stall control click --x <pixels> --y <pixels> [--host-config <path>] [--target-profile <name>] [--host <id>] [--dry-run]
  maya-stall evidence collect [--host-config <path>] [--target-profile <name>] [--host <id>] <scenario>
  maya-stall evidence publish --destination <path> --base-url <url> <evidence-bundle-dir>
  maya-stall review-comment github --repo <owner/name> --pr <number> [--token-env <name>] [--api-url <url>] [--dry-run] <published-evidence-dir>
  maya-stall review-comment gitlab --project <path-or-id> --merge-request <iid> [--token-env <name>] [--base-url <url>] [--dry-run] <published-evidence-dir>
  maya-stall status [--run <run-id>]
  maya-stall attach <run-id>
  maya-stall stop <run-id>

Commands:
  attach   print kept run events and logs
  control click   send an explicit desktop click through the Session Broker
  doctor   check local config, Target Profile, and Host Health layers
  evidence collect   run a Scenario and write a complete Evidence Bundle
  evidence publish   copy an Evidence Bundle to a filesystem Evidence Store
  init      write a repo-only sample .maya-stall.yaml
  record   capture a Session Broker recording artifact
  review-comment   create or update a GitHub PR or GitLab MR Review Comment
  run       run a named Scenario with fake or configured SSH transport
  screenshot   capture a Session Broker screenshot artifact
  status   show kept run state
  stop     stop a kept run and release its Host Lock
  version   print the maya-stall version
`)
}

type runOptions struct {
	ScenarioName  string
	HostConfig    string
	TargetProfile string
	HostPin       string
	HostLockWait  time.Duration
	StopAfter     string
}

func parseRunArgs(args []string) (runOptions, error) {
	options := runOptions{TargetProfile: "default", StopAfter: stopAfterAlways}
	stopPolicySet := false
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
		case "--keep-on-failure":
			if stopPolicySet {
				return runOptions{}, newUsageError("Stop Policy already set")
			}
			options.StopAfter = stopAfterSuccess
			stopPolicySet = true
		case "--stop-after":
			if stopPolicySet {
				return runOptions{}, newUsageError("Stop Policy already set")
			}
			i++
			if i >= len(args) || args[i] == "" {
				return runOptions{}, newUsageError("--stop-after needs success, failure, always, or never")
			}
			if !isValidStopAfter(args[i]) {
				return runOptions{}, newUsageError("invalid --stop-after %q", args[i])
			}
			options.StopAfter = args[i]
			stopPolicySet = true
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
		Now: time.Now,
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
`

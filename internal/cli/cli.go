package cli

import (
	"encoding/json"
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
	case "plan":
		options, err := parsePlanArgs(args[1:])
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "maya-stall plan: %v\n", err)
			return 2
		}
		plan, err := buildScenarioPlan(workDir, options)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "maya-stall plan: %v\n", err)
			var userErr *usageError
			if errors.As(err, &userErr) {
				return 2
			}
			return 1
		}
		printScenarioPlan(stdout, plan, options.JSON)
		if !plan.Ready {
			return 1
		}
		return 0
	case "run":
		options, err := parseRunArgs(args[1:])
		jsonOutput := requestedRunJSON(args[1:])
		if err != nil {
			if options.ScenarioName != "" {
				runtime = withRunAcceptanceOutput(runtime, stdout, jsonOutput)
				outcome, acceptedErr := failAcceptedSubmission(workDir, options, runtime, err)
				if outcome.RunID != "" {
					printRunCommandOutcome(stdout, outcome, jsonOutput)
				}
				_, _ = fmt.Fprintf(stderr, "maya-stall run: %v\n", acceptedErr)
				return 1
			}
			if jsonOutput {
				printRunCommandJSON(stdout, runCommandJSON{Version: 1, Kind: "usage-error", Accepted: false, Error: err.Error()})
				return 2
			}
			fmt.Fprintf(stderr, "maya-stall run: %v\n", err)
			return 2
		}
		runtime = withRunAcceptanceOutput(runtime, stdout, jsonOutput)
		outcome, err := runScenario(workDir, options, runtime)
		if err != nil {
			if outcome.RunID != "" {
				printRunCommandOutcome(stdout, outcome, jsonOutput)
				_, _ = fmt.Fprintf(stderr, "maya-stall run: %v\n", err)
				return 1
			}
			var userErr *usageError
			if errors.As(err, &userErr) {
				fmt.Fprintf(stderr, "maya-stall run: %v\n", err)
				return 2
			}
			fmt.Fprintf(stderr, "maya-stall run: %v\n", err)
			return 1
		}
		printRunCommandOutcome(stdout, outcome, jsonOutput)
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
			_, _ = fmt.Fprintf(stderr, "maya-stall control: %v\n", err)
			return 2
		}
		outcome, err := runDesktopControl(workDir, options, runtime)
		if err != nil {
			var userErr *usageError
			if errors.As(err, &userErr) {
				_, _ = fmt.Fprintf(stderr, "maya-stall control: %v\n", err)
				return 2
			}
			_, _ = fmt.Fprintf(stderr, "maya-stall control: %v\n", err)
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
			jsonOutput := requestedRunJSON(args[2:])
			if err != nil {
				if options.ScenarioName != "" {
					runtime = withRunAcceptanceOutput(runtime, stdout, jsonOutput)
					outcome, acceptedErr := failAcceptedSubmission(workDir, options, runtime, err)
					if outcome.RunID != "" {
						printRunCommandOutcome(stdout, outcome, jsonOutput)
					}
					_, _ = fmt.Fprintf(stderr, "maya-stall evidence collect: %v\n", acceptedErr)
					return 1
				}
				if jsonOutput {
					printRunCommandJSON(stdout, runCommandJSON{Version: 1, Kind: "usage-error", Accepted: false, Error: err.Error()})
					return 2
				}
				fmt.Fprintf(stderr, "maya-stall evidence collect: %v\n", err)
				return 2
			}
			runtime = withRunAcceptanceOutput(runtime, stdout, jsonOutput)
			outcome, err := runScenario(workDir, options, runtime)
			if err != nil {
				if outcome.RunID != "" {
					printRunCommandOutcome(stdout, outcome, jsonOutput)
					_, _ = fmt.Fprintf(stderr, "maya-stall evidence collect: %v\n", err)
					return 1
				}
				var userErr *usageError
				if errors.As(err, &userErr) {
					fmt.Fprintf(stderr, "maya-stall evidence collect: %v\n", err)
					return 2
				}
				fmt.Fprintf(stderr, "maya-stall evidence collect: %v\n", err)
				return 1
			}
			printRunCommandOutcome(stdout, outcome, jsonOutput)
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
		options, err := parseAttachArgs(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "maya-stall attach: %v\n", err)
			return 2
		}
		if err := runAttachAction(workDir, options, stdout); err != nil {
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
	if outcome.Accepted || outcome.Failure != nil {
		_, _ = fmt.Fprintf(stdout, "accepted: %t\n", outcome.Accepted)
	}
	fmt.Fprintf(stdout, "scenario: %s\n", outcome.Scenario)
	fmt.Fprintf(stdout, "targetProfile: %s\n", outcome.TargetProfile)
	fmt.Fprintf(stdout, "host: %s\n", outcome.Host)
	fmt.Fprintf(stdout, "status: %s\n", outcome.Result.Status)
	fmt.Fprintf(stdout, "stopPolicy: %s\n", outcome.StopPolicy)
	fmt.Fprintf(stdout, "state: %s\n", outcome.StateDir)
	fmt.Fprintf(stdout, "evidence: %s\n", outcome.EvidenceDir)
	if outcome.Failure != nil {
		_, _ = fmt.Fprintf(stdout, "failedLayer: %s\n", outcome.Failure.FailedLayer)
		_, _ = fmt.Fprintf(stdout, "diagnostic: %s\n", outcome.Failure.Diagnostic)
		_, _ = fmt.Fprintf(stdout, "remediation: %s\n", outcome.Failure.RemediationHint)
	}
	for _, validator := range outcome.Validators {
		if validator.Status != resultStatusPassed {
			fmt.Fprintf(stdout, "validator: %s %s - %s\n", validator.Type, validator.Status, validator.Message)
		}
	}
	for _, command := range outcome.FollowUpCommands {
		fmt.Fprintf(stdout, "next: %s\n", command)
	}
}

func withRunAcceptanceOutput(runtime runRuntime, stdout io.Writer, asJSON bool) runRuntime {
	existing := runtime.Accepted
	runtime.Accepted = func(outcome runOutcome) {
		if existing != nil {
			existing(outcome)
		}
		if asJSON {
			printRunCommandJSON(stdout, runCommandJSON{
				Version:     1,
				Kind:        "run-accepted",
				Accepted:    true,
				RunID:       outcome.RunID,
				Scenario:    outcome.Scenario,
				Status:      "submitted",
				StateDir:    outcome.StateDir,
				EvidenceDir: outcome.EvidenceDir,
			})
			return
		}
		_, _ = fmt.Fprintf(stdout, "run: %s\n", outcome.RunID)
		_, _ = fmt.Fprintln(stdout, "accepted: true")
		_, _ = fmt.Fprintf(stdout, "scenario: %s\n", outcome.Scenario)
		_, _ = fmt.Fprintf(stdout, "state: %s\n", outcome.StateDir)
		_, _ = fmt.Fprintf(stdout, "evidence: %s\n", outcome.EvidenceDir)
	}
	return runtime
}

type runCommandJSON struct {
	Version          int      `json:"version"`
	Kind             string   `json:"kind"`
	Accepted         bool     `json:"accepted"`
	RunID            string   `json:"runId,omitempty"`
	Scenario         string   `json:"scenario,omitempty"`
	TargetProfile    string   `json:"targetProfile,omitempty"`
	Host             string   `json:"host,omitempty"`
	Status           string   `json:"status,omitempty"`
	StateDir         string   `json:"stateDir,omitempty"`
	EvidenceDir      string   `json:"evidenceDir,omitempty"`
	FailedLayer      string   `json:"failedLayer,omitempty"`
	Diagnostic       string   `json:"diagnostic,omitempty"`
	RemediationHint  string   `json:"remediationHint,omitempty"`
	StopPolicy       string   `json:"stopPolicy,omitempty"`
	FollowUpCommands []string `json:"followUpCommands,omitempty"`
	Error            string   `json:"error,omitempty"`
}

func printRunCommandOutcome(stdout io.Writer, outcome runOutcome, asJSON bool) {
	if !asJSON {
		printRunOutcome(stdout, outcome)
		return
	}
	result := runCommandJSON{
		Version:          1,
		Kind:             "run",
		Accepted:         outcome.Accepted,
		RunID:            outcome.RunID,
		Scenario:         outcome.Scenario,
		TargetProfile:    outcome.TargetProfile,
		Host:             outcome.Host,
		Status:           outcome.Result.Status,
		StateDir:         outcome.StateDir,
		EvidenceDir:      outcome.EvidenceDir,
		StopPolicy:       outcome.StopPolicy,
		FollowUpCommands: outcome.FollowUpCommands,
	}
	if outcome.Failure != nil {
		result.FailedLayer = outcome.Failure.FailedLayer
		result.Diagnostic = outcome.Failure.Diagnostic
		result.RemediationHint = outcome.Failure.RemediationHint
	}
	printRunCommandJSON(stdout, result)
}

func printRunCommandJSON(stdout io.Writer, result runCommandJSON) {
	_ = json.NewEncoder(stdout).Encode(result)
}

func requestedRunJSON(args []string) bool {
	for _, arg := range args {
		if arg == "--json" {
			return true
		}
	}
	return false
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
  maya-stall doctor [--host-config <path>] [--target-profile <name>] [--host <id>] [--scenario <name>] [--repair-trusted-plugin-allowlist]
  maya-stall plan [--json] [--host-config <path>] <scenario>
  maya-stall run [--json] [--host-config <path>] [--target-profile <name>] [--host <id>] [--host-lock-wait <duration>|--host-lock-fail-fast] [--keep-on-failure|--stop-after <success|failure|always|never>] <scenario>
  maya-stall screenshot [--host-config <path>] [--target-profile <name>] [--host <id>]
  maya-stall record [--host-config <path>] [--target-profile <name>] [--host <id>]
  maya-stall control click --x <pixels> --y <pixels> [--host-config <path>] [--target-profile <name>] [--host <id>] [--dry-run]
  maya-stall evidence collect [--json] [--host-config <path>] [--target-profile <name>] [--host <id>] <scenario>
  maya-stall evidence publish --destination <path> --base-url <url> <evidence-bundle-dir>
  maya-stall review-comment github --repo <owner/name> --pr <number> [--token-env <name>] [--api-url <url>] [--dry-run] <published-evidence-dir>
  maya-stall review-comment gitlab --project <path-or-id> --merge-request <iid> [--token-env <name>] [--base-url <url>] [--dry-run] <published-evidence-dir>
  maya-stall status [--run <run-id>]
  maya-stall attach <run-id>
  maya-stall attach <run-id> screenshot
  maya-stall attach <run-id> control click --x <pixels> --y <pixels>
  maya-stall stop <run-id>

Commands:
  attach   observe a run or perform run-scoped screenshot/control
  control click   send an explicit desktop click through the Session Broker
  doctor   check local config, Target Profile, and Host Health layers
  evidence collect   run a Scenario and write a complete Evidence Bundle
  evidence publish   copy an Evidence Bundle to a filesystem Evidence Store
  init      write a repo-only sample .maya-stall.yaml
  plan      inspect a normalized Scenario and optional host compatibility without host contact
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
		case "--json":
		case "--host-config":
			i++
			if i >= len(args) || args[i] == "" || strings.HasPrefix(args[i], "--") {
				return options, newUsageError("--host-config needs a path")
			}
			options.HostConfig = args[i]
		case "--target-profile":
			i++
			if i >= len(args) || args[i] == "" || strings.HasPrefix(args[i], "--") {
				return options, newUsageError("--target-profile needs a name")
			}
			options.TargetProfile = args[i]
		case "--host":
			i++
			if i >= len(args) || args[i] == "" || strings.HasPrefix(args[i], "--") {
				return options, newUsageError("--host needs a Maya Host id")
			}
			options.HostPin = args[i]
		case "--host-lock-wait":
			i++
			if i >= len(args) || args[i] == "" || strings.HasPrefix(args[i], "--") {
				return options, newUsageError("--host-lock-wait needs a duration")
			}
			duration, err := time.ParseDuration(args[i])
			if err != nil {
				return options, newUsageError("invalid --host-lock-wait duration %q", args[i])
			}
			options.HostLockWait = duration
		case "--host-lock-fail-fast":
			options.HostLockWait = 0
		case "--keep-on-failure":
			if stopPolicySet {
				return options, newUsageError("Stop Policy already set")
			}
			options.StopAfter = stopAfterSuccess
			stopPolicySet = true
		case "--stop-after":
			if stopPolicySet {
				return options, newUsageError("Stop Policy already set")
			}
			i++
			if i >= len(args) || args[i] == "" || strings.HasPrefix(args[i], "--") {
				return options, newUsageError("--stop-after needs success, failure, always, or never")
			}
			if !isValidStopAfter(args[i]) {
				return options, newUsageError("invalid --stop-after %q", args[i])
			}
			options.StopAfter = args[i]
			stopPolicySet = true
		default:
			if strings.HasPrefix(arg, "-") {
				return options, newUsageError("unknown run option %q", arg)
			}
			if options.ScenarioName != "" {
				return options, newUsageError("expected one Scenario name")
			}
			options.ScenarioName = arg
		}
	}
	if options.ScenarioName == "" {
		return options, newUsageError("expected Scenario name")
	}
	return options, nil
}

type doctorOptions struct {
	HostConfig                   string
	TargetProfile                string
	HostPin                      string
	ScenarioName                 string
	RepairTrustedPluginAllowlist bool
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
		case "--repair-trusted-plugin-allowlist":
			options.RepairTrustedPluginAllowlist = true
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

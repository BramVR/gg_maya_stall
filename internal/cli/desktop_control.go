package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"
)

type desktopControlOptions struct {
	HostConfig    string
	TargetProfile string
	HostPin       string
	DryRun        bool
	Action        string
	X             int
	Y             int
}

type desktopControlOutcome struct {
	Action        string
	TargetProfile string
	Host          string
	Runtime       runtimeMetadata
	X             int
	Y             int
	DryRun        bool
}

func parseDesktopControlArgs(args []string) (desktopControlOptions, error) {
	if len(args) == 0 {
		return desktopControlOptions{}, newUsageError("expected control action click")
	}
	if args[0] != "click" {
		return desktopControlOptions{}, newUsageError("unknown control action %q", args[0])
	}
	options := desktopControlOptions{TargetProfile: "default", Action: "click", X: -1, Y: -1}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--host-config":
			i++
			if i >= len(args) || args[i] == "" {
				return desktopControlOptions{}, newUsageError("--host-config needs a path")
			}
			options.HostConfig = args[i]
		case "--target-profile":
			i++
			if i >= len(args) || args[i] == "" {
				return desktopControlOptions{}, newUsageError("--target-profile needs a name")
			}
			options.TargetProfile = args[i]
		case "--host":
			i++
			if i >= len(args) || args[i] == "" {
				return desktopControlOptions{}, newUsageError("--host needs a Maya Host id")
			}
			options.HostPin = args[i]
		case "--dry-run":
			options.DryRun = true
		case "--x":
			i++
			if i >= len(args) || args[i] == "" {
				return desktopControlOptions{}, newUsageError("--x needs a coordinate")
			}
			value, err := strconv.Atoi(args[i])
			if err != nil {
				return desktopControlOptions{}, newUsageError("invalid --x coordinate %q", args[i])
			}
			options.X = value
		case "--y":
			i++
			if i >= len(args) || args[i] == "" {
				return desktopControlOptions{}, newUsageError("--y needs a coordinate")
			}
			value, err := strconv.Atoi(args[i])
			if err != nil {
				return desktopControlOptions{}, newUsageError("invalid --y coordinate %q", args[i])
			}
			options.Y = value
		default:
			return desktopControlOptions{}, newUsageError("unknown control option %q", arg)
		}
	}
	if options.X < 0 || options.Y < 0 {
		return desktopControlOptions{}, newUsageError("desktop click coordinates must be non-negative")
	}
	return options, nil
}

func runDesktopControl(repoDir string, options desktopControlOptions, runtime runRuntime) (outcome desktopControlOutcome, err error) {
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	host, err := selectHostForRun(repoDir, runOptions{
		HostConfig:    options.HostConfig,
		TargetProfile: options.TargetProfile,
		HostPin:       options.HostPin,
	})
	if err != nil {
		return desktopControlOutcome{}, err
	}
	defer func() {
		if host.release != nil {
			if releaseErr := host.release(); releaseErr != nil {
				err = errors.Join(err, fmt.Errorf("release Host Lock for %s: %w", host.HostID, releaseErr))
			}
		}
	}()
	resolved, err := resolveRuntimeForHost(host.Config)
	if err != nil {
		return desktopControlOutcome{}, err
	}
	if runtime.Broker == nil {
		runtime.Broker = resolved.Broker
	}
	outcome = desktopControlOutcome{
		Action:        options.Action,
		TargetProfile: host.TargetProfile,
		Host:          host.HostID,
		Runtime:       resolved.Metadata,
		X:             options.X,
		Y:             options.Y,
		DryRun:        options.DryRun,
	}
	if options.DryRun {
		return outcome, nil
	}
	if err := rejectMismatchedRuntimeOverride(resolved, runtime); err != nil {
		return desktopControlOutcome{}, err
	}
	clicker, ok := runtime.Broker.(desktopClicker)
	if !ok {
		return desktopControlOutcome{}, fmt.Errorf("Session Broker does not support desktop control")
	}
	runID := runtime.Now().UTC().Format("20060102T150405.000000000Z")
	workspace, err := newRunWorkspace(repoDir, runID, host.Config.WorkRoot, "outputs/desktop-control.json")
	if err != nil {
		return desktopControlOutcome{}, err
	}
	remoteRoot := remoteJoin(workspace.RemoteRunRoot(), "desktop-control", "click")
	if err := clicker.ClickDesktop(desktopClickRequest{RemoteRoot: remoteRoot, X: options.X, Y: options.Y}); err != nil {
		return desktopControlOutcome{}, err
	}
	return outcome, nil
}

func printDesktopControlOutcome(stdout io.Writer, outcome desktopControlOutcome) {
	fmt.Fprintf(stdout, "action: %s\n", outcome.Action)
	fmt.Fprintf(stdout, "targetProfile: %s\n", outcome.TargetProfile)
	fmt.Fprintf(stdout, "host: %s\n", outcome.Host)
	fmt.Fprintf(stdout, "runtime: %s\n", outcome.Runtime.Profile)
	fmt.Fprintf(stdout, "x: %d\n", outcome.X)
	fmt.Fprintf(stdout, "y: %d\n", outcome.Y)
	fmt.Fprintf(stdout, "dryRun: %t\n", outcome.DryRun)
}

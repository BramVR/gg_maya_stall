package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	sessiondScenarioTimeout = 10 * time.Minute
	sessiondCommandTimeout  = 2 * time.Minute
)

type ggMayaSessiondBroker struct {
	host mayaHostConfig
}

type sessiondCommandResult struct {
	OK    bool   `json:"ok"`
	Tool  string `json:"tool"`
	Error string `json:"error"`
}

type sessiondCaptureResult struct {
	OK      bool   `json:"ok"`
	Tool    string `json:"tool"`
	Error   string `json:"error"`
	Content []struct {
		Type     string `json:"type"`
		Data     string `json:"data"`
		MimeType string `json:"mimeType"`
	} `json:"content"`
	Output struct {
		MimeType string `json:"mime_type"`
		Format   string `json:"format"`
	} `json:"output"`
}

func sessionBrokerForConfig(host mayaHostConfig) sessionBroker {
	if host.Broker.isGGMayaSessiond() {
		return ggMayaSessiondBroker{host: host}
	}
	if reason := host.Broker.invalidReason(); reason != "" {
		return invalidSessionBroker{err: fmt.Errorf("%s", reason)}
	}
	return fakeSessionBroker{Result: ScenarioResult{Status: resultStatusPassed, Summary: "fake Scenario completed"}}
}

func (broker ggMayaSessiondBroker) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	if err := broker.validate(); err != nil {
		return ScenarioResult{}, err
	}
	if err := appendEvent(context.EventsPath, "broker.session.started", scenario.MayaVersion); err != nil {
		return ScenarioResult{}, err
	}
	if err := os.WriteFile(context.LogPath, []byte("gg_mayasessiond Session Broker ran Scenario\n"), 0o644); err != nil {
		return ScenarioResult{}, err
	}
	runID := filepath.Base(context.StateDir)
	remoteRunRoot := remoteJoin(broker.host.WorkRoot, "runs", runID)
	remoteWorkspace := remoteJoin(remoteRunRoot, "workspace")
	wrapperPath := remoteJoin(remoteWorkspace, ".maya-stall-scenario.py")
	wrapper, err := broker.scenarioWrapper(context, scenario, remoteRunRoot, remoteWorkspace)
	if err != nil {
		return ScenarioResult{}, err
	}
	if err := broker.stageRemoteFile(wrapperPath, []byte(wrapper)); err != nil {
		return ScenarioResult{}, fmt.Errorf("stage gg_mayasessiond Scenario wrapper: %w", err)
	}
	result, err := broker.callTool("script.execute", []string{
		"file_path=" + wrapperPath,
		"timeout=" + strconv.Itoa(int(sessiondScenarioTimeout/time.Second)),
	}, sessiondScenarioTimeout+sshCommandTimeout)
	if err != nil {
		return ScenarioResult{}, err
	}
	if !result.OK {
		return ScenarioResult{}, fmt.Errorf("gg_mayasessiond script.execute failed: %s", result.Error)
	}
	if err := appendEvent(context.EventsPath, "broker.session.finished", "completed"); err != nil {
		return ScenarioResult{}, err
	}
	return ScenarioResult{Status: resultStatusPassed, Summary: "gg_mayasessiond Scenario completed"}, nil
}

func (broker ggMayaSessiondBroker) CaptureScreenshot(context runContext, request screenshotRequest) (visualEvidenceArtifact, error) {
	if err := broker.validate(); err != nil {
		return visualEvidenceArtifact{}, err
	}
	name := filepath.Base(filepath.ToSlash(request.Name))
	result, err := broker.callCapture()
	if err != nil {
		return visualEvidenceArtifact{}, err
	}
	if !result.OK {
		return visualEvidenceArtifact{}, fmt.Errorf("gg_mayasessiond viewport.capture failed: %s", result.Error)
	}
	data, mediaType, err := captureImageData(result)
	if err != nil {
		return visualEvidenceArtifact{}, err
	}
	name = visualEvidenceNameForMediaType(name, mediaType)
	relative := filepath.Join("screenshots", name)
	path := filepath.Join(context.EvidenceDir, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return visualEvidenceArtifact{}, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return visualEvidenceArtifact{}, err
	}
	if err := appendEvent(context.EventsPath, "broker.screenshot.captured", filepath.ToSlash(relative)); err != nil {
		return visualEvidenceArtifact{}, err
	}
	return visualEvidenceArtifact{Kind: "screenshot", Path: filepath.ToSlash(relative), MediaType: mediaType}, nil
}

func (broker ggMayaSessiondBroker) CaptureRecording(runContext, recordingRequest) (visualEvidenceArtifact, error) {
	return visualEvidenceArtifact{}, fmt.Errorf("gg_mayasessiond does not expose recording capture; disable recording evidence or use screenshot/viewport capture")
}

func (broker ggMayaSessiondBroker) validate() error {
	if !broker.host.usesRealSSH() {
		return fmt.Errorf("gg_mayasessiond broker requires transport: ssh")
	}
	if strings.TrimSpace(broker.host.WorkRoot) == "" {
		return fmt.Errorf("gg_mayasessiond broker requires workRoot")
	}
	if strings.TrimSpace(broker.host.Broker.StateDir) == "" {
		return fmt.Errorf("gg_mayasessiond broker requires broker.stateDir")
	}
	if strings.TrimSpace(broker.host.Broker.Python) == "" {
		return fmt.Errorf("gg_mayasessiond broker requires broker.python")
	}
	if strings.TrimSpace(broker.host.Broker.Repo) == "" {
		return fmt.Errorf("gg_mayasessiond broker requires broker.repo")
	}
	return nil
}

func (broker ggMayaSessiondBroker) scenarioWrapper(context runContext, scenario scenarioConfig, remoteRunRoot string, remoteWorkspace string) (string, error) {
	resultPath, err := windowsRemoteRepoPath(remoteWorkspace, scenario.ExpectedOutputs.ScenarioResult)
	if err != nil {
		return "", err
	}
	scripts, err := remotePayloadScripts(remoteRunRoot, scenario.Payload)
	if err != nil {
		return "", err
	}
	includePaths, err := remotePayloadIncludePaths(remoteRunRoot, scenario.Payload)
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	builder.WriteString("import json, os, runpy, sys, traceback\n")
	builder.WriteString("result_path = ")
	builder.WriteString(pythonString(resultPath))
	builder.WriteString("\n")
	builder.WriteString("run_modules_root = ")
	builder.WriteString(pythonString(remoteJoin(broker.host.WorkRoot, "runs")))
	builder.WriteString("\n")
	builder.WriteString("previous_cwd = os.getcwd()\n")
	builder.WriteString("previous_sys_path = list(sys.path)\n")
	builder.WriteString("previous_result_env = os.environ.get(")
	builder.WriteString(pythonString(scenarioResultEnvVar))
	builder.WriteString(")\n")
	builder.WriteString("def _maya_stall_write_result(status, summary, traceback_text=None, overwrite=False):\n")
	builder.WriteString("    if os.path.exists(result_path) and not overwrite:\n")
	builder.WriteString("        return\n")
	builder.WriteString("    payload = {'status': status, 'summary': summary}\n")
	builder.WriteString("    if traceback_text is not None:\n")
	builder.WriteString("        payload['traceback'] = traceback_text\n")
	builder.WriteString("    with open(result_path, 'w', encoding='utf-8') as handle:\n")
	builder.WriteString("        json.dump(payload, handle)\n")
	builder.WriteString("        handle.write('\\n')\n")
	builder.WriteString("def _maya_stall_should_overwrite_failure():\n")
	builder.WriteString("    if not os.path.exists(result_path):\n")
	builder.WriteString("        return True\n")
	builder.WriteString("    try:\n")
	builder.WriteString("        with open(result_path, 'r', encoding='utf-8') as handle:\n")
	builder.WriteString("            payload = json.load(handle)\n")
	builder.WriteString("    except Exception:\n")
	builder.WriteString("        return True\n")
	builder.WriteString("    return payload.get('status') in (None, '', 'passed')\n")
	builder.WriteString("def _maya_stall_clear_run_modules():\n")
	builder.WriteString("    root = os.path.normcase(os.path.abspath(run_modules_root))\n")
	builder.WriteString("    for name, module in list(sys.modules.items()):\n")
	builder.WriteString("        module_file = getattr(module, '__file__', None)\n")
	builder.WriteString("        if not module_file:\n")
	builder.WriteString("            continue\n")
	builder.WriteString("        try:\n")
	builder.WriteString("            module_path = os.path.normcase(os.path.abspath(module_file))\n")
	builder.WriteString("        except Exception:\n")
	builder.WriteString("            continue\n")
	builder.WriteString("        if module_path.startswith(root + os.sep):\n")
	builder.WriteString("            sys.modules.pop(name, None)\n")
	builder.WriteString("try:\n")
	builder.WriteString("    os.environ[")
	builder.WriteString(pythonString(scenarioResultEnvVar))
	builder.WriteString("] = result_path\n")
	builder.WriteString("    os.makedirs(os.path.dirname(result_path), exist_ok=True)\n")
	builder.WriteString("    os.chdir(")
	builder.WriteString(pythonString(remoteWorkspace))
	builder.WriteString(")\n")
	builder.WriteString("    _maya_stall_clear_run_modules()\n")
	builder.WriteString("    for include_path in reversed(")
	builder.WriteString(pythonStringList(includePaths))
	builder.WriteString("):\n        sys.path.insert(0, include_path)\n")
	builder.WriteString("    for script_path in ")
	builder.WriteString(pythonStringList(scripts))
	builder.WriteString(":\n        runpy.run_path(script_path, run_name='__main__')\n")
	builder.WriteString("    _maya_stall_write_result('passed', 'gg_mayasessiond Scenario completed')\n")
	builder.WriteString("except SystemExit as exc:\n")
	builder.WriteString("    code = exc.code\n")
	builder.WriteString("    if code is None or code == 0:\n")
	builder.WriteString("        _maya_stall_write_result('passed', 'gg_mayasessiond Scenario completed')\n")
	builder.WriteString("    else:\n")
	builder.WriteString("        _maya_stall_write_result('failed', 'Scenario exited with code %s' % code, overwrite=_maya_stall_should_overwrite_failure())\n")
	builder.WriteString("except Exception as exc:\n")
	builder.WriteString("    _maya_stall_write_result('failed', str(exc), traceback.format_exc(), overwrite=_maya_stall_should_overwrite_failure())\n")
	builder.WriteString("finally:\n")
	builder.WriteString("    sys.path[:] = previous_sys_path\n")
	builder.WriteString("    os.chdir(previous_cwd)\n")
	builder.WriteString("    if previous_result_env is None:\n")
	builder.WriteString("        os.environ.pop(")
	builder.WriteString(pythonString(scenarioResultEnvVar))
	builder.WriteString(", None)\n")
	builder.WriteString("    else:\n")
	builder.WriteString("        os.environ[")
	builder.WriteString(pythonString(scenarioResultEnvVar))
	builder.WriteString("] = previous_result_env\n")
	builder.WriteString("    _maya_stall_clear_run_modules()\n")
	return builder.String(), nil
}

func remotePayloadScripts(remoteRunRoot string, payload runPayload) ([]string, error) {
	paths := append([]string{}, payload.MayaScripts...)
	paths = append(paths, payload.Scripts...)
	remote := make([]string, 0, len(paths))
	for _, source := range paths {
		clean, err := cleanRepoRelativePath(source)
		if err != nil {
			return nil, err
		}
		remote = append(remote, remoteJoin(remoteRunRoot, "payload", "mayaScripts", clean))
	}
	return remote, nil
}

func remotePayloadIncludePaths(remoteRunRoot string, payload runPayload) ([]string, error) {
	remote := make([]string, 0, len(payload.IncludePaths))
	for _, source := range payload.IncludePaths {
		clean, err := cleanRepoRelativePath(source)
		if err != nil {
			return nil, err
		}
		remote = append(remote, remoteJoin(remoteRunRoot, "payload", "includePaths", clean))
	}
	return remote, nil
}

func windowsRemoteRepoPath(root string, relative string) (string, error) {
	if strings.Contains(relative, `\`) {
		return "", fmt.Errorf("repo paths used by gg_mayasessiond must use forward slashes, not backslashes")
	}
	clean, err := cleanRepoRelativePath(relative)
	if err != nil {
		return "", err
	}
	return remoteJoin(root, clean), nil
}

func (broker ggMayaSessiondBroker) callCapture() (sessiondCaptureResult, error) {
	raw, err := broker.runSessiondCLI([]string{"call", "--state-dir", broker.host.Broker.StateDir, "viewport.capture", "format=jpeg", "width=1024", "height=576", "quality=85", "--json"}, sessiondCommandTimeout)
	if err != nil {
		return sessiondCaptureResult{}, err
	}
	var result sessiondCaptureResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return sessiondCaptureResult{}, fmt.Errorf("parse gg_mayasessiond viewport.capture JSON: %w", err)
	}
	return result, nil
}

func (broker ggMayaSessiondBroker) callTool(tool string, args []string, timeout time.Duration) (sessiondCommandResult, error) {
	cliArgs := []string{"call", "--state-dir", broker.host.Broker.StateDir, tool}
	cliArgs = append(cliArgs, args...)
	cliArgs = append(cliArgs, "--json")
	raw, err := broker.runSessiondCLI(cliArgs, timeout)
	if err != nil {
		return sessiondCommandResult{}, err
	}
	var result sessiondCommandResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return sessiondCommandResult{}, fmt.Errorf("parse gg_mayasessiond %s JSON: %w", tool, err)
	}
	return result, nil
}

func (broker ggMayaSessiondBroker) runSessiondCLI(args []string, timeout time.Duration) ([]byte, error) {
	if err := broker.validate(); err != nil {
		return nil, err
	}
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, powerShellSingleQuoted(arg))
	}
	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath %s
& %s -m gg_maya_sessiond.cli @(%s)`, powerShellSingleQuoted(broker.host.Broker.Repo), powerShellSingleQuoted(broker.host.Broker.Python), strings.Join(quoted, ","))
	raw, err := runSSHCommandOutput(broker.host, encodedPowerShellCommand(script), timeout)
	if err != nil {
		if jsonOutput, ok := sessiondJSONFromFailedOutput(raw); ok {
			return jsonOutput, nil
		}
		return nil, fmt.Errorf("run gg_mayasessiond %s: %w", args[0], err)
	}
	return trimToJSON(raw), nil
}

func (broker ggMayaSessiondBroker) stageRemoteFile(path string, content []byte) error {
	if err := broker.validate(); err != nil {
		return err
	}
	if err := rejectSFTPBatchUnsafePath(path); err != nil {
		return err
	}
	tempFile, err := os.CreateTemp("", "maya-stall-sessiond-*")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if _, err := tempFile.Write(content); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	batch := newSFTPBatch()
	batch.mkdirAll(remoteDir(path))
	batch.put(tempPath, path)
	return runSFTPBatch(broker.host, batch.String())
}

func (broker ggMayaSessiondBroker) probeScriptExecute() (err error) {
	runID := fmt.Sprintf("doctor-%d", time.Now().UTC().UnixNano())
	probeRoot := remoteJoin(broker.host.WorkRoot, "runs", runID)
	probePath := remoteJoin(probeRoot, "workspace", ".maya-stall-doctor.py")
	if err := broker.stageRemoteFile(probePath, []byte("print('maya-stall doctor script.execute ok')\n")); err != nil {
		return fmt.Errorf("stage gg_mayasessiond script.execute probe: %w", err)
	}
	defer func() {
		if cleanupErr := broker.removeRemotePath(probeRoot); cleanupErr != nil {
			cleanupErr = fmt.Errorf("cleanup gg_mayasessiond script.execute probe: %w", cleanupErr)
			if err == nil {
				err = cleanupErr
			} else {
				err = errors.Join(err, cleanupErr)
			}
		}
	}()
	result, err := broker.callTool("script.execute", []string{
		"file_path=" + probePath,
		"timeout=30",
	}, sessiondCommandTimeout)
	if err != nil {
		return fmt.Errorf("run gg_mayasessiond script.execute probe: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("gg_mayasessiond script.execute probe failed: %s", result.Error)
	}
	return nil
}

func (broker ggMayaSessiondBroker) removeRemotePath(path string) error {
	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
Remove-Item -LiteralPath %s -Recurse -Force`, powerShellSingleQuoted(path))
	_, err := runSSHCommandOutput(broker.host, encodedPowerShellCommand(script), sessiondCommandTimeout)
	return err
}

func captureImageData(result sessiondCaptureResult) ([]byte, string, error) {
	mediaType := result.Output.MimeType
	for _, item := range result.Content {
		if item.Data == "" {
			continue
		}
		if item.MimeType != "" {
			mediaType = item.MimeType
		}
		data, err := base64.StdEncoding.DecodeString(item.Data)
		if err != nil {
			return nil, "", fmt.Errorf("decode viewport.capture image data: %w", err)
		}
		if mediaType == "" {
			mediaType = "image/jpeg"
		}
		return data, mediaType, nil
	}
	return nil, "", fmt.Errorf("gg_mayasessiond viewport.capture returned no image data")
}

func visualEvidenceNameForMediaType(name string, mediaType string) string {
	if name == "" || name == "." || name == ".." {
		name = "screenshot"
	}
	extension := ".jpg"
	switch mediaType {
	case "image/png":
		extension = ".png"
	case "image/jpeg", "":
		extension = ".jpg"
	default:
		extension = ".bin"
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if base == "" || base == "." || base == ".." {
		base = "screenshot"
	}
	return base + extension
}

func pythonString(value string) string {
	content, _ := json.Marshal(value)
	return string(content)
}

func pythonStringList(values []string) string {
	content, _ := json.Marshal(values)
	return string(content)
}

func trimToJSON(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	start := bytes.IndexAny(trimmed, "{[")
	if start < 0 {
		return trimmed
	}
	return trimmed[start:]
}

func sessiondJSONFromFailedOutput(raw []byte) ([]byte, bool) {
	jsonOutput := trimToJSON(raw)
	var object map[string]any
	if err := json.Unmarshal(jsonOutput, &object); err != nil {
		return nil, false
	}
	if _, ok := object["ok"]; !ok {
		return nil, false
	}
	okValue, ok := object["ok"].(bool)
	if !ok || okValue {
		return nil, false
	}
	return jsonOutput, true
}

func runSSHCommandOutput(host mayaHostConfig, remoteCommand []string, timeout time.Duration) ([]byte, error) {
	binary := host.SSH.Binary
	if binary == "" {
		binary = "ssh"
	}
	if timeout <= 0 {
		timeout = sshCommandTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, append(sshArgs(host), remoteCommand...)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return stdout.Bytes(), fmt.Errorf("ssh command timed out after %s", timeout)
		}
		detail := firstUsefulStderrLine(stderr.String())
		if detail != "" {
			return stdout.Bytes(), fmt.Errorf("ssh command failed: %w: %s", err, detail)
		}
		return stdout.Bytes(), fmt.Errorf("ssh command failed: %w", err)
	}
	return stdout.Bytes(), nil
}

func firstUsefulStderrLine(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#< CLIXML") || strings.HasPrefix(line, "<Objs ") {
			continue
		}
		if len(line) > 240 {
			return line[:240] + "..."
		}
		return line
	}
	return ""
}

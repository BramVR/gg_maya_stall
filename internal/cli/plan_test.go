package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanJSONUsesRunScenarioContractWithoutCreatingRunState(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("print('planned')\n")
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "planned.py"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	config := `version: 1
scenarios:
  smoke:
    mayaVersion: "2025"
    payload:
      scripts:
        - scripts/./planned.py
    expectedOutputs:
      scenarioResult: outputs/result.json
`
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"plan", "--json", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("plan exit code = %d, stderr = %q", code, stderr.String())
	}

	var got scenarioPlan
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode plan JSON: %v\n%s", err, stdout.String())
	}
	if got.Version != 1 || got.Kind != "scenario-plan" || got.Scenario != "smoke" || !got.Ready {
		t.Fatalf("plan identity/readiness = %+v", got)
	}
	if len(got.Payload) != 1 {
		t.Fatalf("plan payload = %+v, want one item", got.Payload)
	}
	wantHash := sha256.Sum256(payload)
	want := planPayload{
		Kind:        "mayaScripts",
		Source:      "scripts/planned.py",
		Destination: "payload/mayaScripts/scripts/planned.py",
		Size:        int64(len(payload)),
		SHA256:      hex.EncodeToString(wantHash[:]),
		Status:      "ready",
	}
	if got.Payload[0] != want {
		t.Fatalf("plan payload = %+v, want %+v", got.Payload[0], want)
	}
	if _, err := os.Stat(filepath.Join(dir, ".maya-stall", "state")); !os.IsNotExist(err) {
		t.Fatalf("plan created run state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "artifacts", "maya-stall")); !os.IsNotExist(err) {
		t.Fatalf("plan created evidence state: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version"); code != 0 {
		t.Fatalf("run exit code = %d, stderr = %q", code, stderr.String())
	}
	manifests, err := filepath.Glob(filepath.Join(dir, "artifacts", "maya-stall", "*", "manifest.json"))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("run manifests = %v, err = %v", manifests, err)
	}
	content, err := os.ReadFile(manifests[0])
	if err != nil {
		t.Fatal(err)
	}
	var manifest runManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Payload) != 1 || manifest.Payload[0].Kind != got.Payload[0].Kind || manifest.Payload[0].Source != got.Payload[0].Source || filepath.ToSlash(manifest.Payload[0].Staged) != got.Payload[0].Destination {
		t.Fatalf("run payload = %+v, plan payload = %+v", manifest.Payload, got.Payload)
	}
}

func TestPlanSummarizesDirectoryPayloadDeterministically(t *testing.T) {
	dir := t.TempDir()
	includeDir := filepath.Join(dir, "scripts", "include")
	if err := os.MkdirAll(filepath.Join(includeDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"a.py":        []byte("a = 1\n"),
		"nested/b.py": []byte("b = 2\n"),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(includeDir, filepath.FromSlash(name)), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	size, hash, err := summarizePlanPayload(includeDir)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(files["a.py"])+len(files["nested/b.py"])) {
		t.Fatalf("directory size = %d", size)
	}
	digest := sha256.New()
	for _, name := range []string{"a.py", "nested/b.py"} {
		_, _ = digest.Write([]byte(name))
		_, _ = digest.Write([]byte{0})
		_, _ = fmt.Fprintf(digest, "%d", len(files[name]))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(files[name])
	}
	wantHash := hex.EncodeToString(digest.Sum(nil))
	if hash != wantHash {
		t.Fatalf("directory sha256 = %s, want %s", hash, wantHash)
	}
}

func TestPlanReportsMissingPayloadWithoutCreatingState(t *testing.T) {
	dir := t.TempDir()
	config := `version: 1
scenarios:
  smoke:
    payload:
      scenes:
        - scenes/missing.ma
    expectedOutputs:
      scenarioResult: outputs/result.json
`
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"plan", "--json", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 || stderr.Len() != 0 {
		t.Fatalf("plan missing input exit = %d, stderr = %q", code, stderr.String())
	}
	var got scenarioPlan
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Ready || len(got.Payload) != 1 || got.Payload[0].Status != "missing" || len(got.Issues) != 1 {
		t.Fatalf("missing-input plan = %+v", got)
	}
	if got.Issues[0].Reason != `payload path "scenes/missing.ma" does not exist` {
		t.Fatalf("missing-input reason = %q", got.Issues[0].Reason)
	}
	if _, err := os.Stat(filepath.Join(dir, ".maya-stall")); !os.IsNotExist(err) {
		t.Fatalf("plan created state for missing input: %v", err)
	}
}

func TestPlanReportsUnsafePayloadAsStructuredIssue(t *testing.T) {
	dir := t.TempDir()
	config := `version: 1
scenarios:
  smoke:
    payload:
      scripts:
        - ../outside.py
    expectedOutputs:
      scenarioResult: outputs/result.json
`
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"plan", "--json", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 || stderr.Len() != 0 {
		t.Fatalf("unsafe plan exit = %d, stderr = %q", code, stderr.String())
	}
	var got scenarioPlan
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode unsafe plan: %v\n%s", err, stdout.String())
	}
	if got.Ready || len(got.Issues) != 1 || got.Issues[0].Source != "../outside.py" {
		t.Fatalf("unsafe plan = %+v", got)
	}
	if len(got.Payload) != 1 || got.Payload[0].Source != "../outside.py" || got.Payload[0].Status != "unsafe" {
		t.Fatalf("unsafe payload = %+v", got.Payload)
	}
}

func TestPlanReportsStaticTargetProfileAndHostCompatibility(t *testing.T) {
	dir := t.TempDir()
	repoConfig := `version: 1
scenarios:
  smoke:
    mayaVersion: "2025"
    evidence:
      screenshots:
        enabled: true
    expectedOutputs:
      scenarioResult: outputs/result.json
`
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(repoConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	hostConfig := `version: 1
targetProfiles:
  ci:
    hostPool: windows
hostPools:
  windows:
    hosts:
      - id: maya-win-01
        fakeInstalledMayaVersions: ["2025"]
        visualEvidence: true
      - id: maya-win-old
        fakeInstalledMayaVersions: ["2024"]
        visualEvidence: true
      - id: maya-win-offline
        health: unhealthy
        fakeInstalledMayaVersions: ["2025"]
        visualEvidence: true
`
	hostConfigPath := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(hostConfigPath, []byte(hostConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := buildScenarioPlan(dir, planOptions{ScenarioName: "smoke", HostConfig: hostConfigPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.TargetProfiles) != 1 || !plan.TargetProfiles[0].Compatible {
		t.Fatalf("target profiles = %+v", plan.TargetProfiles)
	}
	hosts := plan.TargetProfiles[0].Hosts
	if len(hosts) != 3 || !hosts[0].Compatible || hosts[1].Compatible || hosts[2].Compatible {
		t.Fatalf("host compatibility = %+v", hosts)
	}
	if hosts[1].Reasons[0] != "Scenario requires exact Maya build 2025; reported session build is 2024; installed builds are 2024" {
		t.Fatalf("old-host reasons = %+v", hosts[1].Reasons)
	}
	if hosts[2].Reasons[0] != "Maya Host health is unhealthy" {
		t.Fatalf("offline-host reasons = %+v", hosts[2].Reasons)
	}
}

func TestPlanMatchesExactMayaBuildFromStructuredCapabilities(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(`version: 1
scenarios:
  smoke:
    requirements:
      maya:
        exact: "2025.3"
    expectedOutputs:
      scenarioResult: outputs/result.json
`), 0o644); err != nil {
		t.Fatal(err)
	}
	hostConfigPath := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(hostConfigPath, []byte(`version: 1
targetProfiles:
  ci:
    hostPool: windows
hostPools:
  windows:
    hosts:
      - id: maya-win-01
        capabilities:
          mayaBuilds: ["2025.3"]
          python: "3.11.9"
          sessionBroker:
            version: "1"
            features: ["script.execute"]
          capture: []
          control: []
          renderers: ["unknown"]
          gpu: ["unknown"]
          display: ["console"]
          licensing: ["available"]
          trustedPluginArtifacts: false
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := buildScenarioPlan(dir, planOptions{ScenarioName: "smoke", HostConfig: hostConfigPath})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Requirements.HostCapabilities.Maya.Exact != "2025.3" {
		t.Fatalf("normalized plan requirements = %+v", plan.Requirements)
	}
	if !plan.Ready || len(plan.TargetProfiles) != 1 || !plan.TargetProfiles[0].Hosts[0].Compatible {
		t.Fatalf("exact Maya capability plan = %+v", plan)
	}
}

func TestPlanRejectsPythonBelowMinimumVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(`version: 1
scenarios:
  smoke:
    requirements:
      python:
        minimum: "3.11"
    expectedOutputs:
      scenarioResult: outputs/result.json
`), 0o644); err != nil {
		t.Fatal(err)
	}
	hostConfigPath := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(hostConfigPath, []byte(`version: 1
targetProfiles:
  ci:
    hostPool: windows
hostPools:
  windows:
    hosts:
      - id: maya-win-01
        capabilities:
          mayaBuilds: ["2025.3"]
          python: "3.10.12"
          sessionBroker:
            version: "1"
            features: ["script.execute"]
          capture: []
          control: []
          renderers: ["unknown"]
          gpu: ["unknown"]
          display: ["console"]
          licensing: ["available"]
          trustedPluginArtifacts: false
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := buildScenarioPlan(dir, planOptions{ScenarioName: "smoke", HostConfig: hostConfigPath})
	if err != nil {
		t.Fatal(err)
	}
	host := plan.TargetProfiles[0].Hosts[0]
	if plan.Ready || host.Compatible || !strings.Contains(strings.Join(host.Reasons, "\n"), "Python requires minimum 3.11; reported version is 3.10.12") {
		t.Fatalf("minimum Python capability plan = %+v", plan)
	}
}

func TestPlanExplainsEveryCapabilityMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(`version: 1
scenarios:
  smoke:
    requirements:
      maya:
        minimum: "2025.2"
      python:
        exact: "3.11.9"
      sessionBroker:
        minimum: "2.1"
        features: ["script.execute", "status.observe"]
      capture: ["recording"]
      control: ["semantic"]
      renderers: ["arnold"]
      gpu: ["nvidia"]
      display: ["console"]
      licensing: ["available"]
      trustedPluginArtifacts: true
    expectedOutputs:
      scenarioResult: outputs/result.json
`), 0o644); err != nil {
		t.Fatal(err)
	}
	hostConfigPath := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(hostConfigPath, []byte(`version: 1
targetProfiles:
  ci:
    hostPool: windows
hostPools:
  windows:
    hosts:
      - id: maya-win-01
        capabilities:
          mayaBuilds: ["2025.1"]
          python: "3.10"
          sessionBroker:
            version: "2.0"
            features: ["script.execute"]
          capture: ["screenshot"]
          control: ["coordinate"]
          renderers: ["maya-software"]
          gpu: ["amd"]
          display: ["services"]
          licensing: ["unavailable"]
          trustedPluginArtifacts: false
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := buildScenarioPlan(dir, planOptions{ScenarioName: "smoke", HostConfig: hostConfigPath})
	if err != nil {
		t.Fatal(err)
	}
	host := plan.TargetProfiles[0].Hosts[0]
	joined := strings.Join(host.Reasons, "\n")
	for _, wanted := range []string{
		"Maya requires minimum 2025.2",
		"Python requires exact 3.11.9",
		"Session Broker requires minimum 2.1",
		"Session Broker feature status.observe is required",
		"capture capability recording is required",
		"control capability semantic is required",
		"renderer capability arnold is required",
		"GPU capability nvidia is required",
		"display capability console is required",
		"licensing capability available is required",
		"trusted Plugin Artifact support must be true",
	} {
		if !strings.Contains(joined, wanted) {
			t.Fatalf("capability reasons missing %q: %+v", wanted, host.Reasons)
		}
	}
}

func TestPlanRejectsAmbiguousVersionRequirements(t *testing.T) {
	tests := []struct {
		name        string
		requirement string
		want        string
	}{
		{
			name: "exact and minimum",
			requirement: `    requirements:
      python:
        exact: "3.11.9"
        minimum: "3.11"
`,
			want: "Python requirement cannot set both exact and minimum",
		},
		{
			name: "legacy and structured Maya",
			requirement: `    mayaVersion: "2025"
    requirements:
      maya:
        minimum: "2025.2"
`,
			want: "Scenario cannot combine mayaVersion with requirements.maya",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			config := "version: 1\nscenarios:\n  smoke:\n" + test.requirement + "    expectedOutputs:\n      scenarioResult: outputs/result.json\n"
			if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(config), 0o644); err != nil {
				t.Fatal(err)
			}
			plan, err := buildScenarioPlan(dir, planOptions{ScenarioName: "smoke"})
			if err != nil {
				t.Fatal(err)
			}
			if plan.Ready || len(plan.Issues) == 0 || !strings.Contains(plan.Issues[0].Reason, test.want) {
				t.Fatalf("ambiguous requirement plan = %+v, want %q", plan, test.want)
			}
		})
	}
}

func TestPlanHumanOutputIncludesRequirementsAndTargetReasons(t *testing.T) {
	plan := scenarioPlan{
		Version:  1,
		Kind:     "scenario-plan",
		Scenario: "smoke",
		Ready:    true,
		Requirements: planRequirements{
			MayaVersion:   "2025",
			SessionBroker: true,
			Capabilities:  []string{"script.execute", "screenshot.capture"},
			HostCapabilities: scenarioRequirements{
				Python: versionRequirement{Minimum: "3.11"}, Capture: []string{"screenshot"},
			},
		},
		Payload: []planPayload{{Kind: "scenes", Source: "scene.ma", Destination: "payload/scenes/scene.ma", Size: 12, SHA256: "abc", Status: "ready"}},
		TargetProfiles: []planTargetProfile{{
			Name: "ci", HostPool: "windows", Compatible: true,
			Hosts: []planHost{
				{ID: "maya-win-01", Compatible: true},
				{ID: "maya-win-old", Reasons: []string{"Scenario needs Maya 2025; configured inventory has 2024"}},
			},
		}},
	}
	var output bytes.Buffer
	printScenarioPlan(&output, plan, false)
	for _, wanted := range []string{
		"capability: script.execute",
		"capability: screenshot.capture",
		"python-version: minimum 3.11",
		"required-capture: screenshot",
		"target-profile: ci (Host Pool windows) [compatible]",
		"host: maya-win-01 [compatible]",
		"host: maya-win-old [incompatible: Scenario needs Maya 2025; configured inventory has 2024]",
		"host-contact: none",
	} {
		if !strings.Contains(output.String(), wanted+"\n") {
			t.Fatalf("human plan missing %q:\n%s", wanted, output.String())
		}
	}
}

func TestPlanWithRealHostConfigNeverInvokesHostOrBroker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(`version: 1
scenarios:
  smoke:
    mayaVersion: "2025"
    expectedOutputs:
      scenarioResult: outputs/result.json
`), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "contacted")
	binary := filepath.Join(dir, "must-not-run")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\ntouch \""+marker+"\"\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	hostConfig := `version: 1
targetProfiles:
  ci:
    hostPool: windows
hostPools:
  windows:
    hosts:
      - id: maya-win-01
        transport: ssh
        ssh:
          host: maya-win-01
          binary: ` + binary + `
          sftpBinary: ` + binary + `
        workRoot: C:/maya-stall
        mayaVersions: ["2025"]
        broker:
          type: gg-mayasessiond
          stateDir: C:/maya-stall/sessiond-ui
          python: C:/Python/python.exe
          repo: C:/maya-stall/broker
`
	hostConfigPath := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(hostConfigPath, []byte(hostConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"plan", "--json", "--host-config", hostConfigPath, "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("plan exit = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("plan contacted configured host or broker: %v", err)
	}
}

func TestPlanUnknownScenarioIsUsageError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(`version: 1
scenarios:
  smoke:
    expectedOutputs:
      scenarioResult: outputs/result.json
`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"plan", "missing"}, &stdout, &stderr, dir, "test-version")
	if code != 2 || !strings.Contains(stderr.String(), `unknown Scenario "missing"`) {
		t.Fatalf("unknown Scenario exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestPlanHostCompatibilityIncludesAllMutationFreeRunValidation(t *testing.T) {
	base := mayaHostConfig{
		ID:           "maya-win-01",
		Transport:    "ssh",
		SSH:          sshConfig{Host: "maya-win-01"},
		WorkRoot:     "C:/maya-stall",
		MayaVersions: []string{"2025"},
		Broker: brokerConfig{
			Structured: true,
			Type:       "gg-mayasessiond",
			StateDir:   "C:/maya-stall/sessiond-ui",
			Python:     "C:/Python/python.exe",
			Repo:       "C:/maya-stall/broker",
		},
	}
	tests := []struct {
		name   string
		mutate func(*mayaHostConfig)
		want   string
	}{
		{
			name: "invalid SFTP timeout",
			mutate: func(host *mayaHostConfig) {
				host.SSH.SFTPTimeout = "bogus"
			},
			want: `ssh.sftpTimeout "bogus" must be a Go duration`,
		},
		{
			name: "unsafe trusted root",
			mutate: func(host *mayaHostConfig) {
				host.TrustedPluginArtifactsRoot = "relative/plugins"
			},
			want: "trustedPluginArtifactsRoot must be an absolute Windows path",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := base
			tt.mutate(&host)
			planned := planHostCompatibility("", host, scenarioConfig{MayaVersion: "2025"}, nil)
			if planned.Compatible || !strings.Contains(strings.Join(planned.Reasons, "\n"), tt.want) {
				t.Fatalf("host compatibility = %+v, want reason containing %q", planned, tt.want)
			}
		})
	}
}

func TestPlanRejectsEntireProfileWhenAnyHostIDIsInvalid(t *testing.T) {
	config := userHostConfig{
		Version:        1,
		TargetProfiles: map[string]targetProfileConfig{"ci": {HostPool: "windows"}},
		HostPools: map[string]hostPoolConfig{"windows": {Hosts: []mayaHostConfig{
			{ID: "alpha"},
			{ID: "bad/id"},
		}}},
	}
	profiles := planTargetCompatibility("", config, scenarioConfig{}, nil)
	if len(profiles) != 1 || profiles[0].Compatible || !strings.Contains(strings.Join(profiles[0].Reasons, "\n"), `Maya Host id "bad/id"`) {
		t.Fatalf("profile compatibility = %+v", profiles)
	}
}

func TestPlanRejectsTransportUnsafeScenarioPaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	unsafeSource := "scripts/unsafe\nname.py"
	if err := os.WriteFile(filepath.Join(dir, unsafeSource), []byte("pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := `version: 1
scenarios:
  smoke:
    payload:
      scripts:
        - "scripts/unsafe\nname.py"
    expectedOutputs:
      scenarioResult: "outputs/unsafe\nresult.json"
`
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := buildScenarioPlan(dir, planOptions{ScenarioName: "smoke"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ready || len(plan.Issues) == 0 || !strings.Contains(plan.Issues[0].Reason, "unsupported SFTP batch control characters") {
		t.Fatalf("transport-unsafe plan = %+v", plan)
	}
}

func TestPlanHumanOutputEscapesTerminalControlCharacters(t *testing.T) {
	controlValue := "safe\x1b]0;forged\a"
	plan := scenarioPlan{
		Scenario: controlValue,
		Requirements: planRequirements{
			MayaVersion:  controlValue,
			Capabilities: []string{controlValue},
		},
		Payload: []planPayload{{Kind: "scripts", Source: controlValue, Destination: controlValue, Status: "invalid"}},
		Issues:  []planIssue{{Source: controlValue, Reason: controlValue}},
	}
	var output bytes.Buffer
	printScenarioPlan(&output, plan, false)
	if strings.Contains(output.String(), "\x1b") || strings.Contains(output.String(), "\a") {
		t.Fatalf("human plan contains raw terminal controls: %q", output.String())
	}
	if !strings.Contains(output.String(), `\x1b]0;forged\a`) {
		t.Fatalf("human plan did not render escaped controls: %q", output.String())
	}
}

func TestPlanInspectsCleanDeclarationsWhenAnotherDeclarationIsUnsafe(t *testing.T) {
	dir := t.TempDir()
	config := `version: 1
scenarios:
  smoke:
    payload:
      scenes:
        - scenes/missing.ma
        - ../outside.ma
    expectedOutputs:
      scenarioResult: outputs/result.json
`
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := buildScenarioPlan(dir, planOptions{ScenarioName: "smoke"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Payload) != 2 || plan.Payload[0].Status != "missing" || plan.Payload[1].Status != "unsafe" || len(plan.Issues) != 2 {
		t.Fatalf("partially unsafe plan = %+v", plan)
	}
}

func TestPlanRejectsTransportUnsafeValidatorPaths(t *testing.T) {
	dir := t.TempDir()
	config := `version: 1
scenarios:
  smoke:
    expectedOutputs:
      scenarioResult: outputs/result.json
    validators:
      - type: outputExists
        path: "outputs/unsafe\nfile.json"
`
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := buildScenarioPlan(dir, planOptions{ScenarioName: "smoke"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ready || len(plan.Issues) != 1 || !strings.Contains(plan.Issues[0].Reason, "unsupported SFTP batch control characters") {
		t.Fatalf("validator-path plan = %+v", plan)
	}
}

func TestPlanKeepsIndependentScenarioContractAndPayloadIssues(t *testing.T) {
	dir := t.TempDir()
	config := `version: 1
scenarios:
  smoke:
    payload:
      scenes:
        - scenes/missing.ma
`
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := buildScenarioPlan(dir, planOptions{ScenarioName: "smoke"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Issues) != 2 {
		t.Fatalf("contract + payload issues = %+v", plan.Issues)
	}
	reasons := plan.Issues[0].Reason + "\n" + plan.Issues[1].Reason
	if !strings.Contains(reasons, "missing expectedOutputs.scenarioResult") || !strings.Contains(reasons, "does not exist") {
		t.Fatalf("contract + payload reasons = %q", reasons)
	}
}

func TestPlanIncludesRequiredVisualEvidenceValidatorInRequirements(t *testing.T) {
	disabled := false
	scenario := scenarioConfig{Validators: []validatorConfig{{Type: "visualEvidence"}}}
	capabilities := scenarioPlanCapabilities(scenario)
	if !containsString(capabilities, "visual-evidence.required") {
		t.Fatalf("capabilities = %+v", capabilities)
	}
	planned := planHostCompatibility("", mayaHostConfig{ID: "fake", VisualEvidence: &disabled}, scenario, nil)
	if planned.Compatible || !containsString(planned.Reasons, "Visual Evidence is disabled") {
		t.Fatalf("host compatibility = %+v", planned)
	}

	optional := false
	scenario.Validators[0].Required = &optional
	capabilities = scenarioPlanCapabilities(scenario)
	if containsString(capabilities, "visual-evidence.required") {
		t.Fatalf("optional Visual Evidence capabilities = %+v", capabilities)
	}
}

func TestPlanRejectsConfiguredFakeTransportFailure(t *testing.T) {
	planned := planHostCompatibility("", mayaHostConfig{ID: "fake", SSH: sshConfig{FakeStatus: "unreachable"}}, scenarioConfig{}, nil)
	if planned.Compatible || !containsString(planned.Reasons, `fake SSH transport is "unreachable"`) {
		t.Fatalf("fake transport compatibility = %+v", planned)
	}
}

func TestPlanStillAnalyzesHostConfigWhenScenarioContractIsInvalid(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(`version: 1
scenarios:
  broken: {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	hostConfigPath := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(hostConfigPath, []byte(`version: 1
targetProfiles:
  ci:
    hostPool: windows
hostPools:
  windows:
    hosts:
      - id: fake
`), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := buildScenarioPlan(dir, planOptions{ScenarioName: "broken", HostConfig: hostConfigPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.TargetProfiles) != 1 || !plan.TargetProfiles[0].Compatible || len(plan.Issues) != 1 {
		t.Fatalf("invalid Scenario host analysis = %+v", plan)
	}

	plan, err = buildScenarioPlan(dir, planOptions{ScenarioName: "broken", HostConfig: filepath.Join(dir, "missing-hosts.yaml")})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Issues) != 2 || !strings.Contains(plan.Issues[1].Reason, "missing-hosts.yaml") {
		t.Fatalf("invalid Scenario missing host config = %+v", plan)
	}
}

func TestPlanIncludesStaticTrustedPluginArtifactPreflight(t *testing.T) {
	tests := []struct {
		name        string
		pluginPath  string
		mayaVersion string
		hostVersion string
		wantReason  string
	}{
		{
			name:        "invalid Windows destination",
			pluginPath:  "plugins/bad:name.mll",
			mayaVersion: `    mayaVersion: "2025"` + "\n",
			hostVersion: `        mayaVersions: ["2025"]` + "\n",
			wantReason:  "invalid Windows path component",
		},
		{
			name:       "missing preferences version",
			pluginPath: "plugins/good.mll",
			wantReason: "maya version is required to locate TrustCenter preferences",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(tt.pluginPath)), []byte("plugin"), 0o644); err != nil {
				t.Fatal(err)
			}
			repoConfig := `version: 1
scenarios:
  smoke:
` + tt.mayaVersion + `    payload:
      pluginArtifacts:
        - "` + tt.pluginPath + `"
    expectedOutputs:
      scenarioResult: outputs/result.json
`
			if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(repoConfig), 0o644); err != nil {
				t.Fatal(err)
			}
			hostConfig := `version: 1
targetProfiles:
  ci:
    hostPool: windows
hostPools:
  windows:
    hosts:
      - id: maya-win-01
        transport: ssh
        ssh:
          host: maya-win-01
        workRoot: C:/maya-stall
        trustedPluginArtifactsRoot: C:/maya-stall-trusted/plugins
` + tt.hostVersion + `        broker:
          type: gg-mayasessiond
          stateDir: C:/maya-stall/sessiond-ui
          python: C:/Python/python.exe
          repo: C:/maya-stall/broker
`
			hostConfigPath := filepath.Join(dir, "hosts.yaml")
			if err := os.WriteFile(hostConfigPath, []byte(hostConfig), 0o644); err != nil {
				t.Fatal(err)
			}
			plan, err := buildScenarioPlan(dir, planOptions{ScenarioName: "smoke", HostConfig: hostConfigPath})
			if err != nil {
				t.Fatal(err)
			}
			host := plan.TargetProfiles[0].Hosts[0]
			if host.Compatible || !strings.Contains(strings.Join(host.Reasons, "\n"), tt.wantReason) {
				t.Fatalf("trusted plug-in compatibility = %+v, want %q", host, tt.wantReason)
			}
		})
	}
}

func TestPlanKeepsRemotePathIssuesWhenPayloadContractIsInvalid(t *testing.T) {
	dir := t.TempDir()
	config := `version: 1
scenarios:
  smoke:
    payload:
      scenes:
        - ../outside.ma
    expectedOutputs:
      scenarioResult: outputs/result.json
    validators:
      - type: outputExists
        path: "outputs/unsafe\nfile.json"
`
	if err := os.WriteFile(filepath.Join(dir, ".maya-stall.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := buildScenarioPlan(dir, planOptions{ScenarioName: "smoke"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Issues) != 2 {
		t.Fatalf("payload + remote path issues = %+v", plan.Issues)
	}
	reasons := plan.Issues[0].Reason + "\n" + plan.Issues[1].Reason
	if !strings.Contains(reasons, "repo-relative") || !strings.Contains(reasons, "unsupported SFTP batch control characters") {
		t.Fatalf("payload + remote path reasons = %q", reasons)
	}
}

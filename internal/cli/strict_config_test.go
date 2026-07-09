package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRepoRunConfigRejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantField string
	}{
		{
			name: "top level",
			content: `version: 1
scenarios:
  smoke:
    payload: {}
extraRoot: true
`,
			wantField: "extraRoot",
		},
		{
			name: "nested payload",
			content: `version: 1
scenarios:
  smoke:
    payload:
      scriptz:
        - maya/smoke.py
`,
			wantField: "scriptz",
		},
		{
			name: "misspelled scenario field",
			content: `version: 1
scenarios:
  smoke:
    mayaVerison: "2025"
    payload: {}
`,
			wantField: "mayaVerison",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), test.content)

			_, _, err := loadRepoRunConfig(dir)
			if err == nil {
				t.Fatal("loadRepoRunConfig returned nil error")
			}
			assertStrictYAMLError(t, err, test.wantField)
		})
	}
}

func TestLoadRepoRunConfigAcceptsValidConfig(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    mayaVersion: "2025"
    payload:
      scripts:
        - maya/smoke.py
    expectedOutputs:
      scenarioResult: outputs/result.json
`)

	config, path, err := loadRepoRunConfig(dir)
	if err != nil {
		t.Fatalf("loadRepoRunConfig returned error: %v", err)
	}
	if path != filepath.Join(dir, ".maya-stall.yaml") {
		t.Fatalf("config path = %q, want repo config path", path)
	}
	scenario := config.Scenarios["smoke"]
	if scenario.MayaVersion != "2025" || len(scenario.Payload.Scripts) != 1 || scenario.Payload.Scripts[0] != "maya/smoke.py" {
		t.Fatalf("decoded scenario = %+v, want valid smoke config", scenario)
	}
}

func TestLoadUserHostConfigRejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantField string
	}{
		{
			name: "top level",
			content: `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools: {}
extraRoot: true
`,
			wantField: "extraRoot",
		},
		{
			name: "nested host",
			content: `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        workRoot: C:/maya-stall
        workDir: C:/wrong
`,
			wantField: "workDir",
		},
		{
			name: "misspelled host field",
			content: `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        mayaVerison: "2025"
`,
			wantField: "mayaVerison",
		},
		{
			name: "ssh block",
			content: `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        transport: ssh
        ssh:
          host: maya-win-01
          hostname: wrong
`,
			wantField: "hostname",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ci-hosts.yaml")
			mustWriteFile(t, path, test.content)

			_, err := loadUserHostConfig(path)
			if err == nil {
				t.Fatal("loadUserHostConfig returned nil error")
			}
			assertStrictYAMLError(t, err, test.wantField)
		})
	}
}

func TestLoadUserHostConfigAcceptsValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, path, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        health: healthy
        transport: ssh
        ssh:
          host: maya-win-01
          user: maya-runner
          port: 2222
          identityFile: ~/.ssh/maya-stall-ci
          binary: ssh
          sftpBinary: sftp
          sftpTimeout: 30s
        workRoot: C:/maya-stall
        broker:
          type: gg-mayasessiond
          stateDir: C:/maya-stall/sessiond-ui
          python: C:/maya-stall/sessiond-venv311/Scripts/python.exe
          repo: C:/maya-stall/tools/GG_MayaSessiond
          recoveryTask: MayaStallSessiondUI
        mayaVersions: ["2025"]
`)

	config, err := loadUserHostConfig(path)
	if err != nil {
		t.Fatalf("loadUserHostConfig returned error: %v", err)
	}
	host := config.HostPools["windows-maya"].Hosts[0]
	if host.SSH.Host != "maya-win-01" || host.SSH.Port != 2222 || host.SSH.SFTPTimeout != "30s" {
		t.Fatalf("decoded ssh config = %+v, want valid structured ssh config", host.SSH)
	}
	if !host.Broker.Structured || !host.Broker.isGGMayaSessiond() || host.Broker.RecoveryTask != "MayaStallSessiondUI" {
		t.Fatalf("decoded broker config = %+v, want structured gg_mayasessiond config", host.Broker)
	}
}

func TestLoadUserHostConfigAcceptsYAMLMergeKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, path, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: template
        ssh: &sshDefaults
          user: maya-runner
          port: 2222
        broker: &brokerDefaults
          type: gg-mayasessiond
          python: python
      - id: alpha
        transport: ssh
        ssh:
          <<: *sshDefaults
          host: maya-win-01
        broker:
          <<: *brokerDefaults
          stateDir: C:/maya-stall/sessiond-ui
`)

	config, err := loadUserHostConfig(path)
	if err != nil {
		t.Fatalf("loadUserHostConfig returned error: %v", err)
	}
	host := config.HostPools["windows-maya"].Hosts[1]
	if host.SSH.Host != "maya-win-01" || host.SSH.User != "maya-runner" || host.SSH.Port != 2222 {
		t.Fatalf("decoded merged ssh config = %+v, want inherited defaults and host override", host.SSH)
	}
	if !host.Broker.Structured || !host.Broker.isGGMayaSessiond() || host.Broker.Python != "python" {
		t.Fatalf("decoded merged broker config = %+v, want inherited structured defaults", host.Broker)
	}
}

func TestLoadUserHostConfigRejectsUnknownFieldsInYAMLMerge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, path, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        ssh:
          <<:
            user: maya-runner
            hostname: wrong
          host: maya-win-01
`)

	_, err := loadUserHostConfig(path)
	if err == nil {
		t.Fatal("loadUserHostConfig returned nil error")
	}
	assertStrictYAMLError(t, err, "hostname")
}

func TestLoadUserHostConfigRejectsExplicitMergeTagOnUnknownField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, path, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        ssh:
          !!merge hostname: wrong
          host: maya-win-01
`)

	_, err := loadUserHostConfig(path)
	if err == nil {
		t.Fatal("loadUserHostConfig returned nil error")
	}
	assertStrictYAMLError(t, err, "hostname")
}

func TestLoadUserHostConfigRejectsCyclicYAMLMerge(t *testing.T) {
	tests := map[string]string{
		"mapping": `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        ssh: &loop
          <<: *loop
          host: maya-win-01
`,
		"sequence": `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        ssh:
          <<: &loop [*loop]
          host: maya-win-01
`,
	}

	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ci-hosts.yaml")
			mustWriteFile(t, path, content)

			_, err := loadUserHostConfig(path)
			if err == nil {
				t.Fatal("loadUserHostConfig returned nil error")
			}
			if !strings.Contains(err.Error(), "cyclic YAML merge") {
				t.Fatalf("cyclic YAML merge error = %q, want cycle error", err)
			}
		})
	}
}

func assertStrictYAMLError(t *testing.T, err error, field string) {
	t.Helper()
	message := err.Error()
	for _, want := range []string{"parse ", "line ", "field " + field} {
		if !strings.Contains(message, want) {
			t.Fatalf("strict YAML error = %q, want %q", message, want)
		}
	}
}

package cli

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type repoRunConfig struct {
	Version   int                       `yaml:"version"`
	Scenarios map[string]scenarioConfig `yaml:"scenarios"`
}

type scenarioConfig struct {
	Description     string          `yaml:"description"`
	MayaVersion     string          `yaml:"mayaVersion"`
	Payload         runPayload      `yaml:"payload"`
	ExpectedOutputs expectedOutputs `yaml:"expectedOutputs"`
	Evidence        evidenceConfig  `yaml:"evidence"`
}

type runPayload struct {
	Scripts         []string `yaml:"scripts"`
	Scenes          []string `yaml:"scenes"`
	PluginArtifacts []string `yaml:"pluginArtifacts"`
}

type expectedOutputs struct {
	Files          []string `yaml:"files"`
	ScenarioResult string   `yaml:"scenarioResult"`
}

type evidenceConfig struct {
	Screenshots evidenceToggle `yaml:"screenshots"`
	Recording   evidenceToggle `yaml:"recording"`
}

type evidenceToggle struct {
	Enabled bool `yaml:"enabled"`
}

func loadRepoRunConfig(dir string) (repoRunConfig, string, error) {
	path, err := DiscoverConfig(dir)
	if err != nil {
		return repoRunConfig{}, "", err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return repoRunConfig{}, "", err
	}
	var config repoRunConfig
	if err := yaml.Unmarshal(content, &config); err != nil {
		return repoRunConfig{}, "", fmt.Errorf("parse %s: %w", path, err)
	}
	if config.Version != 1 {
		return repoRunConfig{}, "", fmt.Errorf("unsupported repo config version %d", config.Version)
	}
	if len(config.Scenarios) == 0 {
		return repoRunConfig{}, "", fmt.Errorf("repo config has no Scenarios")
	}
	return config, path, nil
}

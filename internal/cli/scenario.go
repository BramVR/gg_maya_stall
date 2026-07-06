package cli

import (
	"fmt"
	"path/filepath"
)

type scenarioContract struct {
	Name               string
	Config             scenarioConfig
	Payload            []manifestPayload
	ScenarioResultPath string
	Outputs            []scenarioOutputPath
}

type scenarioOutputPath struct {
	Path     string
	Optional bool
}

func resolveScenarioContract(config repoRunConfig, name string) (scenarioContract, error) {
	raw, ok := config.Scenarios[name]
	if !ok {
		return scenarioContract{}, newUsageError("unknown Scenario %q", name)
	}
	scenario, err := normalizeScenarioConfig(name, raw)
	if err != nil {
		return scenarioContract{}, err
	}
	payload, err := buildManifestPayload(scenario.Payload)
	if err != nil {
		return scenarioContract{}, err
	}
	return scenarioContract{
		Name:               name,
		Config:             scenario,
		Payload:            payload,
		ScenarioResultPath: scenario.ExpectedOutputs.ScenarioResult,
		Outputs:            scenarioOutputPaths(scenario),
	}, nil
}

func normalizeScenarioConfig(name string, scenario scenarioConfig) (scenarioConfig, error) {
	if scenario.ExpectedOutputs.ScenarioResult == "" {
		return scenarioConfig{}, fmt.Errorf("Scenario %q missing expectedOutputs.scenarioResult", name)
	}
	scenarioResultPath, err := cleanScenarioPath(scenario.ExpectedOutputs.ScenarioResult)
	if err != nil {
		return scenarioConfig{}, err
	}
	scenario.ExpectedOutputs.ScenarioResult = scenarioResultPath

	payload, err := normalizeRunPayload(scenario.Payload)
	if err != nil {
		return scenarioConfig{}, err
	}
	scenario.Payload = payload

	files, err := normalizeScenarioPaths(scenario.ExpectedOutputs.Files)
	if err != nil {
		return scenarioConfig{}, err
	}
	scenario.ExpectedOutputs.Files = files

	validators, err := normalizeValidators(scenario.Validators)
	if err != nil {
		return scenarioConfig{}, err
	}
	scenario.Validators = validators

	return scenario, nil
}

func normalizeRunPayload(payload runPayload) (runPayload, error) {
	var err error
	if payload.MayaScripts, err = normalizeScenarioPaths(payload.MayaScripts); err != nil {
		return runPayload{}, err
	}
	if payload.Scripts, err = normalizeScenarioPaths(payload.Scripts); err != nil {
		return runPayload{}, err
	}
	if payload.Scenes, err = normalizeScenarioPaths(payload.Scenes); err != nil {
		return runPayload{}, err
	}
	if payload.PluginArtifacts, err = normalizeScenarioPaths(payload.PluginArtifacts); err != nil {
		return runPayload{}, err
	}
	if payload.ExpectedOutputs, err = normalizeScenarioPaths(payload.ExpectedOutputs); err != nil {
		return runPayload{}, err
	}
	if payload.IncludePaths, err = normalizeScenarioPaths(payload.IncludePaths); err != nil {
		return runPayload{}, err
	}
	return payload, nil
}

func normalizeValidators(validators []validatorConfig) ([]validatorConfig, error) {
	normalized := make([]validatorConfig, 0, len(validators))
	for _, validator := range validators {
		if validator.Path != "" {
			path, err := cleanScenarioPath(validator.Path)
			if err != nil {
				return nil, err
			}
			validator.Path = path
		}
		switch validator.Type {
		case "scenarioResultStatus", "visualEvidence":
		case "outputExists", "jsonEquals", "numericApprox", "fileHash":
			if validator.Path == "" {
				return nil, fmt.Errorf("%s Validator missing path", validator.Type)
			}
		default:
			return nil, fmt.Errorf("unknown Validator type %q", validator.Type)
		}
		normalized = append(normalized, validator)
	}
	return normalized, nil
}

func normalizeScenarioPaths(paths []string) ([]string, error) {
	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		clean, err := cleanScenarioPath(path)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, clean)
	}
	return normalized, nil
}

func cleanScenarioPath(path string) (string, error) {
	clean, err := cleanRepoRelativePath(path)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(clean), nil
}

func scenarioOutputPaths(scenario scenarioConfig) []scenarioOutputPath {
	seen := make(map[string]bool)
	var outputs []scenarioOutputPath
	add := func(path string, optional bool) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		outputs = append(outputs, scenarioOutputPath{Path: path, Optional: optional})
	}
	add(scenario.ExpectedOutputs.ScenarioResult, false)
	for _, path := range scenario.ExpectedOutputs.Files {
		add(path, true)
	}
	for _, validator := range scenario.Validators {
		if validator.Path != "" {
			add(validator.Path, true)
		}
	}
	return outputs
}

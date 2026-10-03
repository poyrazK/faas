package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const defaultGithubSetupDependabotPath = ".github/dependabot.yml"

type githubSetupFileChange struct {
	Path     string
	Existing []byte
	Exists   bool
	Desired  []byte
	Changed  bool
}

func prepareGithubActionsDependabot(path string) (githubSetupFileChange, error) {
	existing, exists, err := readGithubSetupFile(path)
	if err != nil {
		return githubSetupFileChange{}, err
	}
	desired, changed, err := ensureGithubActionsDependabot(existing, exists)
	if err != nil {
		return githubSetupFileChange{}, err
	}
	return githubSetupFileChange{
		Path:     path,
		Existing: existing,
		Exists:   exists,
		Desired:  desired,
		Changed:  changed,
	}, nil
}

// ensureGithubActionsDependabot adds a weekly root GitHub Actions ecosystem
// entry while leaving existing Dependabot ecosystems and their settings
// intact. An existing root Actions entry is treated as user-managed.
func ensureGithubActionsDependabot(existing []byte, exists bool) ([]byte, bool, error) {
	if !exists {
		return []byte("# Keep pinned GitHub Actions dependencies up to date.\nversion: 2\nupdates:\n  - package-ecosystem: github-actions\n    directory: /\n    schedule:\n      interval: weekly\n"), true, nil
	}

	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(existing))
	if err := decoder.Decode(&document); err != nil {
		return nil, false, fmt.Errorf("parse existing Dependabot config: %w", err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, false, errors.New("existing Dependabot config must contain one YAML document")
		}
		return nil, false, fmt.Errorf("parse existing Dependabot config: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, false, errors.New("existing Dependabot config must be a YAML mapping")
	}
	root := document.Content[0]
	version, versionCount := mappingValue(root, "version")
	if versionCount != 1 {
		return nil, false, errors.New("existing Dependabot config must contain one version: 2 entry")
	}
	if version.Kind != yaml.ScalarNode || version.Value != "2" {
		return nil, false, errors.New("existing Dependabot config must contain one version: 2 entry")
	}
	updates, updatesCount := mappingValue(root, "updates")
	if updatesCount != 1 {
		return nil, false, errors.New("existing Dependabot config must contain one updates sequence")
	}
	if updates.Kind != yaml.SequenceNode {
		return nil, false, errors.New("existing Dependabot config must contain one updates sequence")
	}

	rootActions := 0
	for _, update := range updates.Content {
		if update.Kind != yaml.MappingNode {
			return nil, false, errors.New("existing Dependabot updates entries must be mappings")
		}
		ecosystem, ecosystemCount := mappingValue(update, "package-ecosystem")
		if ecosystemCount != 1 || ecosystem.Kind != yaml.ScalarNode || ecosystem.Value == "" {
			return nil, false, errors.New("each Dependabot updates entry must contain one package-ecosystem value")
		}
		directory, directoryCount := mappingValue(update, "directory")
		if directoryCount != 1 || directory.Kind != yaml.ScalarNode || directory.Value == "" {
			return nil, false, errors.New("each Dependabot updates entry must contain one directory value")
		}
		schedule, scheduleCount := mappingValue(update, "schedule")
		if scheduleCount != 1 || schedule.Kind != yaml.MappingNode {
			return nil, false, errors.New("each Dependabot updates entry must contain one schedule mapping")
		}
		interval, intervalCount := mappingValue(schedule, "interval")
		if intervalCount != 1 || interval.Kind != yaml.ScalarNode || interval.Value == "" {
			return nil, false, errors.New("each Dependabot schedule must contain one interval value")
		}
		if ecosystem.Value != "github-actions" {
			continue
		}
		if directory.Value == "/" {
			rootActions++
		}
	}
	if rootActions > 1 {
		return nil, false, errors.New("existing Dependabot config contains more than one root GitHub Actions entry")
	}
	if rootActions == 1 {
		return existing, false, nil
	}

	updates.Content = append(updates.Content, githubActionsDependabotUpdateNode())
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, false, fmt.Errorf("render merged Dependabot config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, false, fmt.Errorf("finish merged Dependabot config: %w", err)
	}
	return output.Bytes(), !bytes.Equal(existing, output.Bytes()), nil
}

func mappingValue(mapping *yaml.Node, key string) (*yaml.Node, int) {
	var value *yaml.Node
	count := 0
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, 0
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Kind == yaml.ScalarNode && mapping.Content[i].Value == key {
			value = mapping.Content[i+1]
			count++
		}
	}
	return value, count
}

func githubActionsDependabotUpdateNode() *yaml.Node {
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "package-ecosystem"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "github-actions"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "directory"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "/"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "schedule"},
			{
				Kind: yaml.MappingNode,
				Tag:  "!!map",
				Content: []*yaml.Node{
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: "interval"},
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: "weekly"},
				},
			},
		},
	}
}

func writeGithubSetupFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create generated-file directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".gregale-setup-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary generated file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0o644); err != nil {
		cleanup()
		return fmt.Errorf("set generated-file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temporary generated file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync generated file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temporary generated file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("commit generated file: %w", err)
	}
	return nil
}

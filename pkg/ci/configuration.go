package ci

import (
	"errors"
	"maps"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateJobConfiguration(job Job) error {
	if !validWorkingDirectory(job.WorkingDirectory) || !validEnvironment(job.Env) {
		return errors.New("invalid environment or working directory")
	}
	for _, step := range job.Steps {
		if !validWorkingDirectory(step.WorkingDirectory) || !validEnvironment(step.Env) {
			return errors.New("invalid step environment or working directory")
		}
		env := maps.Clone(job.Env)
		if env == nil {
			env = make(map[string]string)
		}
		maps.Copy(env, step.Env)
		if !validEnvironment(env) {
			return errors.New("effective step environment exceeds its bounds")
		}
	}
	return nil
}

func validEnvironment(env map[string]string) bool {
	if len(env) > api.CIMaxEnvironmentVariables {
		return false
	}
	total := 0
	for name, value := range env {
		if len(name) > api.CIEnvironmentNameMaxBytes || !environmentNamePattern.MatchString(name) || reservedEnvironmentName(name) ||
			len(value) > api.CIEnvironmentValueMaxBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return false
		}
		total += len(name) + 1 + len(value) + 1 // NAME=value and terminating NUL.
	}
	return total <= api.CIEnvironmentMaxBytes
}

func reservedEnvironmentName(name string) bool {
	name = strings.ToUpper(name)
	for _, prefix := range []string{"FAAS_", "GREGALE_", "LD_", "DYLD_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	switch name {
	case "PATH", "HOME", "LANG", "CI", "PWD", "OLDPWD", "SHELL", "ENV", "BASH_ENV", "SHELLOPTS", "BASHOPTS", "CDPATH", "IFS":
		return true
	}
	return false
}

func validWorkingDirectory(dir string) bool {
	if dir == "" || dir == "." {
		return true
	}
	if len(dir) > api.CIWorkingDirectoryMaxBytes || !utf8.ValidString(dir) || path.IsAbs(dir) || strings.ContainsAny(dir, "\\\x00\r\n\t") {
		return false
	}
	for _, part := range strings.Split(dir, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// Configuration values are strings, including in YAML. Do not silently coerce
// numbers or booleans into a configuration different from the reviewed source.
func stringConfigurationNodes(node *yaml.Node) error {
	if len(node.Content) != 1 {
		return nil // Other malformed shapes are rejected by the typed decoder.
	}
	jobs := mappingValue(node.Content[0], "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return nil
	}
	for i := 1; i < len(jobs.Content); i += 2 {
		job := jobs.Content[i]
		if err := stringConfigurationNode(job); err != nil {
			return err
		}
		if err := stringMatrixNode(mappingValue(job, "matrix")); err != nil {
			return err
		}
		steps := mappingValue(job, "steps")
		if steps != nil && steps.Kind == yaml.SequenceNode {
			for _, step := range steps.Content {
				if err := stringConfigurationNode(step); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func stringMatrixNode(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return errors.New("CI matrix must be a mapping of named variants")
	}
	for i := 1; i < len(node.Content); i += 2 {
		variant := node.Content[i]
		if variant.Kind != yaml.MappingNode {
			return errors.New("CI matrix variants must be mappings")
		}
		if err := stringEnvironmentNode(mappingValue(variant, "env")); err != nil {
			return err
		}
		image := mappingValue(variant, "image")
		if image != nil && (image.Kind != yaml.ScalarNode || image.Tag != "!!str") {
			return errors.New("CI matrix image must be a string")
		}
	}
	return nil
}

func stringConfigurationNode(node *yaml.Node) error {
	if err := stringEnvironmentNode(mappingValue(node, "env")); err != nil {
		return err
	}
	directory := mappingValue(node, "working_directory")
	if directory != nil && (directory.Kind != yaml.ScalarNode || directory.Tag != "!!str") {
		return errors.New("CI working_directory must be a string")
	}
	return nil
}

func mappingValue(node *yaml.Node, name string) *yaml.Node {
	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Value == name {
				return node.Content[i+1]
			}
		}
	}
	return nil
}

func stringEnvironmentNode(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return errors.New("CI env must be a mapping of strings")
	}
	for i := 1; i < len(node.Content); i += 2 {
		if node.Content[i].Kind != yaml.ScalarNode || node.Content[i].Tag != "!!str" {
			return errors.New("CI env values must be strings")
		}
	}
	return nil
}

func jobsManifestVersion(job Job) int {
	if len(job.Env) > 0 || job.WorkingDirectory != "" {
		return 3
	}
	for _, step := range job.Steps {
		if len(step.Env) > 0 || step.WorkingDirectory != "" {
			return 3
		}
	}
	if len(job.Inputs) > 0 {
		return 2
	}
	return 1
}

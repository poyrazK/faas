package main

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// composePublicBuildContexts returns the build contexts, relative to dir, of
// the Compose services in dir that publish ports and build from source. An
// OpenAPI document at a Compose root describes the public service, and "." —
// the root holding every service — failed `gregale test --validate` with "no
// deployable source found". Unpublished services are internal, so they are
// not the API the document describes.
func composePublicBuildContexts(dir string) []string {
	file := composeFileIn(dir)
	if file == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return nil
	}
	var doc struct {
		Services map[string]struct {
			Build any   `yaml:"build"`
			Ports []any `yaml:"ports"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(raw, &doc) != nil {
		return nil
	}
	var contexts []string
	for _, service := range doc.Services {
		if len(service.Ports) == 0 {
			continue
		}
		context := ""
		switch build := service.Build.(type) {
		case string:
			context = build
		case map[string]any:
			context, _ = build["context"].(string)
			if context == "" && build != nil {
				context = "."
			}
		}
		context = path.Clean(filepath.ToSlash(strings.TrimSpace(context)))
		if context == "" || context == "." || strings.HasPrefix(context, "../") || path.IsAbs(context) || strings.Contains(context, "://") {
			continue
		}
		if !slices.Contains(contexts, context) {
			contexts = append(contexts, context)
		}
	}
	slices.Sort(contexts)
	return contexts
}

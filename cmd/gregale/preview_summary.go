package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/deploydiff"
)

// Shared, additive CLI metadata. The underlying plans retain their own
// schemas and validation guarantees, including saved scale-plan checks.
type cliPreviewSummary struct {
	Verification      string `json:"verification"`
	Profile           string `json:"profile"`
	ProfileSource     string `json:"profile_source"`
	App               string `json:"app"`
	AppSource         string `json:"app_source,omitempty"`
	Environment       string `json:"environment"`
	EnvironmentSource string `json:"environment_source,omitempty"`
	Source            string `json:"source,omitempty"`
	LocalChanges      string `json:"local_changes,omitempty"`
	Limits            string `json:"limits"`
}

func newCLIPreview(verification, app, environment, source, changes, limits string) *cliPreviewSummary {
	profileSource := "selected connection"
	if profileOverride != "" {
		profileSource = "explicit --profile"
	}
	return &cliPreviewSummary{Verification: verification, Profile: currentProfile(), ProfileSource: profileSource, App: app, Environment: scopeOrDefault(environment), Source: source, LocalChanges: changes, Limits: limits}
}

func deploymentPreviewTargetSources(preview *cliPreviewSummary, source string, nameExplicit, environmentExplicit bool) {
	preview.AppSource, preview.EnvironmentSource = "source directory name", "default scope"
	if nameExplicit {
		preview.AppSource = "explicit --name"
	}
	if environmentExplicit {
		preview.EnvironmentSource = "explicit --environment"
	}
	if nameExplicit && environmentExplicit {
		return
	}
	link, _, err := findProjectContext(source)
	if err == nil {
		if !nameExplicit && link.App != "" {
			preview.AppSource = "saved source context"
		}
		if !environmentExplicit && link.Environment != "" {
			preview.EnvironmentSource = "saved source context"
		}
	}
}

func renderCLIPreview(w io.Writer, preview *cliPreviewSummary) {
	if preview == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "Preview: %s\n  Connection: %s\n  App: %s\n  Environment: %s\n", preview.Verification, preview.Profile, preview.App, preview.Environment)
	_, _ = fmt.Fprintf(w, "  Connection selection: %s\n", preview.ProfileSource)
	if preview.AppSource != "" {
		_, _ = fmt.Fprintf(w, "  Target selection: app from %s; environment from %s\n", preview.AppSource, preview.EnvironmentSource)
	}
	if preview.Source != "" {
		_, _ = fmt.Fprintf(w, "  Source: %s\n", preview.Source)
	}
	if preview.LocalChanges != "" {
		_, _ = fmt.Fprintf(w, "  Local changes: %s\n", preview.LocalChanges)
	}
	_, _ = fmt.Fprintf(w, "  Verification limits: %s\n", preview.Limits)
}

func renderDiffPreviewJSON(w io.Writer, diff deploydiff.Diff, preview *cliPreviewSummary) error {
	if preview == nil {
		return deploydiff.RenderJSON(w, diff)
	}
	var original bytes.Buffer
	if err := deploydiff.RenderJSON(&original, diff); err != nil {
		return err
	}
	// Append one field to the renderer's object while preserving every
	// existing field, its order, and the engine's sorted nested arrays.
	body := bytes.TrimSpace(original.Bytes())
	metadata, err := json.Marshal(preview)
	if err != nil {
		return err
	}
	combined := append(append(append([]byte{}, body[:len(body)-1]...), []byte(",\"preview\":")...), metadata...)
	combined = append(combined, '}')
	var output bytes.Buffer
	if err := json.Indent(&output, combined, "", "  "); err != nil {
		return err
	}
	output.WriteByte('\n')
	_, err = w.Write(output.Bytes())
	return err
}

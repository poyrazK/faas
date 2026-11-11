package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// devModeSelection is local decision metadata, never sync-history telemetry.
// A setup receipt describes a plan; developer_mode events describe a setting
// confirmed by the session API before uploading source.
type devModeSelection struct {
	Mode    string `json:"mode"`
	Source  string `json:"source"`
	Command string `json:"command,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

func resolveDevMode(root string, config devSourceConfig, manifest *gregalemanifest.Manifest, explicit map[string]bool, watch bool, command string) (devModeSelection, error) {
	selection := devModeSelection{Mode: "build", Source: "default"}
	if config.shape == shapeFunction {
		selection.Mode, selection.Reason = "function", "function_runner"
	}
	if explicit["watch"] && !watch && explicit["watch-command"] {
		return selection, errors.New("--watch=false cannot be combined with --watch-command")
	}
	if explicit["watch-command"] && strings.TrimSpace(command) == "" {
		return selection, errors.New("--watch-command needs a nonempty development command")
	}
	applyDevWatchManifestDefault(manifest, explicit, &watch, &command)
	switch {
	case explicit["watch"] || explicit["watch-command"]:
		selection.Source = "flag"
	case manifest != nil && manifest.Dev != nil && manifest.Dev.Watch != nil:
		selection.Source = "manifest"
	case config.shape != shapeFunction:
		var err error
		command, selection.Reason, err = automaticDevWatchCommand(root, manifest)
		if err != nil {
			return selection, err
		}
		if command != "" {
			watch, selection.Source = true, "detected"
		}
	}
	resolved, err := resolveDevWatch(root, config, watch, command)
	if err != nil {
		return selection, err
	}
	if resolved != nil {
		selection.Mode, selection.Command, selection.Reason = "watch", resolved.Command, ""
	}
	return selection, nil
}

func automaticDevWatchCommand(root string, manifest *gregalemanifest.Manifest) (string, string, error) {
	if manifest != nil && manifest.Build != nil {
		return "", "dockerfile", nil
	}
	if _, err := os.Stat(filepath.Join(root, "Dockerfile")); err == nil {
		return "", "dockerfile", nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("check developer Dockerfile: %w", err)
	}
	profile, err := frameworkprofile.AnalyzeDir(root)
	if err != nil {
		return "", "", err
	}
	if profile.Family != "node" && profile.Family != "python" {
		return "", "unsupported_runtime", nil
	}
	if command := devWatchSuggestion(root); command != "" {
		return command, "", nil
	}
	return "", "no_development_command", nil
}

func (s devModeSelection) watch() *api.DevWatch {
	if s.Mode != "watch" {
		return nil
	}
	return &api.DevWatch{Command: s.Command}
}

// Only a typed watch rejection permits an automatic fallback. This rejection
// precedes session mutation on the server. Transport/5xx/unknown outcomes must
// never cause a second submission, nor may explicit choices silently fall back.
func upsertDevSessionWithMode(client *Client, project string, req api.UpsertDevSessionRequest, mode devModeSelection) (api.DevSessionResponse, devModeSelection, error) {
	req.Watch = mode.watch()
	session, err := upsertDevSession(client, project, req)
	var apiErr *APIError
	if err != nil && mode.Source == "detected" && errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusUnprocessableEntity && apiErr.Problem.Code == api.CodeDevWatchUnsupported {
		mode = devModeSelection{Mode: "build", Source: "server", Reason: apiErr.Problem.Code, Detail: apiErr.Problem.Detail}
		req.Watch = nil
		session, err = upsertDevSession(client, project, req)
	}
	if err != nil {
		return session, mode, err
	}
	if mode.Source == "detected" && req.Watch != nil && session.Watch == nil {
		// Older servers can ignore the request field. A successful response
		// reporting watch off is the existing build path, not proof of HMR.
		mode = devModeSelection{Mode: "build", Source: "server", Reason: "watch_not_confirmed", Detail: "the control plane reports watch mode off"}
		return session, mode, nil
	}
	if (session.Watch == nil) != (req.Watch == nil) || (session.Watch != nil && session.Watch.Command != req.Watch.Command) {
		return session, mode, errors.New("the control plane did not confirm the selected development mode; inspect the developer session and update the control plane before retrying")
	}
	return session, mode, nil
}

func (s devModeSelection) render(w io.Writer) {
	if s.Mode == "watch" {
		PrintProgress(w, "Development mode: hot reload (%s); command: %s", s.Source, s.Command)
		PrintProgress(w, "Runs your development server. Use --watch=false for a production-shaped build.")
		return
	}
	if s.Mode == "function" {
		PrintProgress(w, "Development mode: function runner (%s)", s.Source)
		return
	}
	PrintProgress(w, "Development mode: production-shaped build (%s)", s.Source)
	if s.Detail != "" {
		PrintProgress(w, "Hot reload unavailable: %s", s.Detail)
	} else if s.Source == "default" {
		reasons := map[string]string{
			"dockerfile":             "Dockerfile builds use their configured start command",
			"unsupported_runtime":    "automatic hot reload supports Node.js and Python apps",
			"no_development_command": "no supported development command was detected",
		}
		if reason := reasons[s.Reason]; reason != "" {
			PrintProgress(w, "Hot reload unavailable: %s", reason)
		}
	}
}

func reportDevMode(mode devModeSelection) error {
	if !jsonOutput {
		mode.render(osStdout)
		return nil
	}
	event := struct {
		Type string `json:"type"`
		devModeSelection
	}{Type: "developer_mode", devModeSelection: mode}
	return writeNDJSON([]any{event})
}

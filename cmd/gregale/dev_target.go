package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// devTarget identifies the developer environment a `gregale dev`
// subcommand acts on. Project and workspace together select the same
// stable app the watch loop created, so read-only and trigger
// subcommands never need the generated dev-* slug from the user.
type devTarget struct {
	Project     string
	SourceDir   string
	WorkspaceID string
}

// devTargetError keeps the terminal title next to the cause so every
// caller renders the same three-line error the watch loop always has.
type devTargetError struct {
	title string
	err   error
}

func (e *devTargetError) print() int { return printErr(e.title, e.err) }

// selectDevProject applies the shared developer project naming rule: an
// explicit --name wins, then the linked app, then the source directory
// name. linkedErr is the result of linkedProjectContext; a missing link is
// the normal unlinked case, any other read failure is surfaced only when
// the link would actually be consulted.
func selectDevProject(name, sourceDir string, linked localProjectContext, linkedErr error) (string, *devTargetError) {
	project := name
	if project == "" {
		switch {
		case linkedErr == nil:
			if linked.App == "" {
				return "", &devTargetError{"No app selected", fmt.Errorf("linked project %q has multiple or no workloads; pass --name or relink with --app <slug>", linked.Project)}
			}
			project = linked.App
		case errors.Is(linkedErr, errProjectContextNotFound):
			project = sanitizeSlug(filepath.Base(sourceDir))
		default:
			return "", &devTargetError{"Could not read local project context", linkedErr}
		}
	}
	if project != sanitizeSlug(project) || len(project) < 3 || len(project) > 40 {
		return "", &devTargetError{"Invalid --name", fmt.Errorf("use 3–40 lowercase letters, digits, and hyphens")}
	}
	return project, nil
}

// resolveDevTarget resolves --path/--name exactly as `gregale dev` does
// from the current directory, then derives the local workspace identity.
// It performs no network calls.
func resolveDevTarget(sourcePath, name string) (devTarget, *devTargetError) {
	cwd, err := os.Getwd()
	if err != nil {
		return devTarget{}, &devTargetError{"Could not read current directory", err}
	}
	sourceDir, err := resolveDeploySourceDir(cwd, sourcePath)
	if err != nil {
		return devTarget{}, &devTargetError{"Invalid developer source", err}
	}
	var linked localProjectContext
	var linkedErr error
	if name == "" {
		linked, _, linkedErr = linkedProjectContext(cwd)
	}
	project, targetErr := selectDevProject(name, sourceDir, linked, linkedErr)
	if targetErr != nil {
		return devTarget{}, targetErr
	}
	developerID, err := loadOrCreateDeveloperID()
	if err != nil {
		return devTarget{}, &devTargetError{"Could not load local developer identity", err}
	}
	workspaceID, err := deriveDevWorkspaceID(developerID, sourceDir)
	if err != nil {
		return devTarget{}, &devTargetError{"Could not identify developer workspace", err}
	}
	return devTarget{Project: project, SourceDir: sourceDir, WorkspaceID: workspaceID}, nil
}

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

var errReadAppTargetRequired = errors.New("app target is required")

type appPickerEntry struct {
	slug, project, status string
}

func readAppTargetError(err error) int {
	if errors.Is(err, errReadAppTargetRequired) {
		PrintUsage(osStderr, err.Error(), "apps")
		return 1
	}
	if errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintln(osStderr, "App selection canceled.")
		return 130
	}
	if errors.Is(err, io.EOF) {
		_, _ = fmt.Fprintln(osStderr, "App selection closed.")
		return 0
	}
	return printErr("Could not select app", err)
}

// resolveReadAppTarget is deliberately opt-in for read commands. It never
// changes the saved link and never substitutes another app for an explicit one.
func resolveReadAppTarget(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	linked, _, err := linkedProjectContext(cwd)
	if err != nil && !errors.Is(err, errProjectContextNotFound) {
		return "", err // A malformed link must be repaired, not bypassed.
	}
	if err == nil && linked.App != "" {
		return linked.App, nil
	}
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return "", fmt.Errorf("%w; pass an app slug or link with gregale link <project-slug> --app <slug>", errReadAppTargetRequired)
	}
	client, err := authedClient()
	if err != nil {
		return "", err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	entries, err := appPickerEntries(ctx, client, linked.Project)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no apps available; create one with gregale start or pass an explicit app slug")
	}
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	if linked.Project != "" {
		_, _ = fmt.Fprintf(prompt.writer, "Project: %s\n", linked.Project)
	}
	if linked.Environment != "" {
		// App reads are not environment-specific reads. Label the saved scope
		// explicitly rather than claiming every listed app runs there.
		_, _ = fmt.Fprintf(prompt.writer, "Linked environment: %s\n", linked.Environment)
	}
	return chooseReadApp(ctx, prompt, entries)
}

func appPickerEntries(ctx context.Context, client *Client, projectSlug string) ([]appPickerEntry, error) {
	entries := []appPickerEntry{}
	if projectSlug != "" {
		project, err := client.GetProject(ctx, projectSlug)
		if err != nil {
			return nil, fmt.Errorf("list linked project apps: %w", err)
		}
		for _, app := range project.Workloads {
			entries = append(entries, appPickerEntry{slug: app.Slug, project: projectSlug, status: app.Status})
		}
	} else {
		apps, err := client.ListApps(ctx)
		if err != nil {
			return nil, fmt.Errorf("list apps: %w", err)
		}
		if len(apps) == 0 {
			return entries, nil
		}
		projects, err := client.ListProjects(ctx)
		if err != nil {
			return nil, fmt.Errorf("list app projects: %w", err)
		}
		projectByApp := map[string]string{}
		for _, summary := range projects {
			project, err := client.GetProject(ctx, summary.Slug)
			if err != nil {
				return nil, fmt.Errorf("list project %s apps: %w", summary.Slug, err)
			}
			for _, app := range project.Workloads {
				projectByApp[app.Slug] = summary.Slug
			}
		}
		for _, app := range apps {
			if app.DeletedAt != nil {
				continue
			}
			entries = append(entries, appPickerEntry{slug: app.Slug, project: projectByApp[app.Slug], status: app.Status})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].slug < entries[j].slug })
	return entries, nil
}

func chooseReadApp(ctx context.Context, prompt *startPrompt, entries []appPickerEntry) (string, error) {
	query := ""
	for {
		matches := []appPickerEntry{}
		_, _ = fmt.Fprintln(prompt.writer, "\nChoose an app (number, exact slug, or search text; / clears search; q cancels).")
		for _, entry := range entries {
			if query != "" && !strings.Contains(strings.ToLower(entry.slug+" "+entry.project+" "+entry.status), query) {
				continue
			}
			matches = append(matches, entry)
			project := entry.project
			if project == "" {
				project = "unattached"
			}
			_, _ = fmt.Fprintf(prompt.writer, "  %d. %s  (project: %s; status: %s)\n", len(matches), entry.slug, project, entry.status)
		}
		if len(matches) == 0 {
			_, _ = fmt.Fprintln(prompt.writer, "No matches. Enter another search or / to show all apps.")
		}
		value, err := prompt.text(ctx, "App", "")
		if err != nil {
			return "", err
		}
		if value == "q" {
			return "", context.Canceled
		}
		if value == "/" {
			query = ""
			continue
		}
		// Exact slugs take precedence over numeric selection.
		for _, entry := range matches {
			if value == entry.slug {
				return entry.slug, nil
			}
		}
		if n, err := strconv.Atoi(value); err == nil {
			if n >= 1 && n <= len(matches) {
				return matches[n-1].slug, nil
			}
			_, _ = fmt.Fprintln(prompt.writer, "Choose a number from the displayed list.")
			continue
		}
		query = strings.ToLower(value)
	}
}

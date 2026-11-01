package main

// commands_resource_labels.go — ADR-20261031100025 step 5 in the CLI:
// `postgres|cron|job label list|set|unset` and the -l selector on their lists.
// The verbs and output are the app's (commands_labels.go). Labels are a
// preview behind the resource-labels capability.

import (
	"context"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/labels"
)

// labelVerbArgs splits `label VERB NAME [LABEL...]` into the verb and its
// labels, which runLabelVerb takes, and the resource's name.
func labelVerbArgs(args []string) (verbArgs []string, name string, ok bool) {
	if len(args) < 2 {
		return nil, "", false
	}
	return append([]string{args[0]}, args[2:]...), args[1], true
}

// cmdPostgresLabel runs `postgres label list|set|unset <database> ...`; the
// database is named by name or id.
func cmdPostgresLabel(args []string) int {
	target := labelTarget{
		noun: "database", doc: "postgres",
		usage: "usage: gregale postgres label list <database> | set <database> KEY=VALUE... | unset <database> KEY...",
	}
	verbArgs, name, ok := labelVerbArgs(args)
	if !ok {
		return runLabelVerb(nil, target)
	}
	target.name = name
	target.read = func(ctx context.Context, client *Client) (map[string]string, error) {
		id, err := postgresDatabaseID(ctx, client, name)
		if err != nil {
			return nil, err
		}
		database, err := client.GetManagedPostgresDatabase(ctx, id)
		return database.Labels, err
	}
	target.write = func(ctx context.Context, client *Client, change api.LabelChange) (api.LabelsResponse, error) {
		id, err := postgresDatabaseID(ctx, client, name)
		if err != nil {
			return api.LabelsResponse{}, err
		}
		return client.UpdateDatabaseLabels(ctx, id, change)
	}
	return runLabelVerb(verbArgs, target)
}

// cmdCronLabel runs `cron label list|set|unset <cron-id> ...`.
func cmdCronLabel(args []string) int {
	target := labelTarget{
		noun: "cron", doc: "crons",
		usage: "usage: gregale cron label list <cron-id> | set <cron-id> KEY=VALUE... | unset <cron-id> KEY...",
	}
	verbArgs, id, ok := labelVerbArgs(args)
	if !ok {
		return runLabelVerb(nil, target)
	}
	target.name = id
	target.read = func(ctx context.Context, client *Client) (map[string]string, error) {
		c, err := client.GetCron(ctx, id)
		return c.Labels, err
	}
	target.write = func(ctx context.Context, client *Client, change api.LabelChange) (api.LabelsResponse, error) {
		return client.UpdateCronLabels(ctx, id, change)
	}
	return runLabelVerb(verbArgs, target)
}

// cmdJobLabel runs `job label list|set|unset <job> ...`.
func cmdJobLabel(args []string) int {
	target := labelTarget{
		noun: "job", doc: "jobs",
		usage: "usage: gregale job label list <job> | set <job> KEY=VALUE... | unset <job> KEY...",
	}
	verbArgs, name, ok := labelVerbArgs(args)
	if !ok {
		return runLabelVerb(nil, target)
	}
	target.name = name
	target.read = func(ctx context.Context, client *Client) (map[string]string, error) {
		job, err := client.GetJob(ctx, name)
		return job.Labels, err
	}
	target.write = func(ctx context.Context, client *Client, change api.LabelChange) (api.LabelsResponse, error) {
		return client.UpdateJobLabels(ctx, name, change)
	}
	return runLabelVerb(verbArgs, target)
}

// parseSelectorFlag parses -l's value; the error names the flag.
func parseSelectorFlag(selector string) (labels.Selector, error) {
	sel, err := labels.Parse(selector)
	if err != nil {
		return labels.Selector{}, fmt.Errorf("--selector: %w", err)
	}
	return sel, nil
}

// selectedDatabases lists the databases selector picks, matching the
// returned labels again as selectedApps does.
func selectedDatabases(ctx context.Context, client *Client, selector string) (api.ManagedPostgresDatabaseList, error) {
	sel, err := parseSelectorFlag(selector)
	if err != nil {
		return api.ManagedPostgresDatabaseList{}, err
	}
	resp, err := client.ListManagedPostgresDatabasesSelected(ctx, sel.String())
	if err != nil {
		return resp, err
	}
	resp.Items = slices.DeleteFunc(resp.Items, func(d api.ManagedPostgresDatabase) bool { return !sel.Matches(d.Labels) })
	return resp, nil
}

// selectedCrons lists the crons selector picks, on one app or every app.
func selectedCrons(ctx context.Context, client *Client, slug, selector string) ([]api.CronResponse, error) {
	sel, err := parseSelectorFlag(selector)
	if err != nil {
		return nil, err
	}
	crons, err := client.ListCronsSelected(ctx, slug, sel.String())
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(crons, func(c api.CronResponse) bool { return !sel.Matches(c.Labels) }), nil
}

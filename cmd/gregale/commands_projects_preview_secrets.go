// adr: 20261104221217
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const projectsPreviewSecretsUsage = "usage: gregale projects preview-secrets <set|unset> <project> (KEY=VALUE... | --from-stdin | KEY...)"

// cmdProjectsPreviewSecrets sets or removes a value in the pr-preview scope of
// every service of a project (ADR-20261104221217). A pull request preview
// receives its production app's pr-preview values (ADR-20261028090000), and
// project environment secrets cannot target previews, so a value every
// preview service needs had to be set once per service.
func cmdProjectsPreviewSecrets(args []string) int {
	if len(args) == 0 || args[0] != "set" && args[0] != "unset" {
		PrintUsage(os.Stderr, projectsPreviewSecretsUsage, "projects")
		return 1
	}
	unset := args[0] == "unset"
	flags, positional := splitArgsForFlags(args[1:], "from-stdin")
	fs := newFlagSet("projects-preview-secrets", flag.ContinueOnError)
	fromStdin := fs.Bool("from-stdin", false, "read KEY=VALUE pairs from stdin (one per line)")
	if err := fs.Parse(flags); err != nil || len(positional) == 0 || !api.ValidProjectSlug(positional[0]) || unset && *fromStdin {
		PrintUsage(os.Stderr, projectsPreviewSecretsUsage, "projects")
		return 1
	}
	project, rest := positional[0], positional[1:]
	pairs, err := previewSecretPairs(unset, *fromStdin, rest)
	if err != nil {
		return printErr("Invalid preview secrets", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.GetProject(context.Background(), project)
	if err != nil {
		return printErr("Could not read project", err)
	}
	var services []api.ProjectWorkloadResponse
	for _, workload := range response.Workloads {
		if workload.Slug != "" && workload.Status != "deleted" {
			services = append(services, workload)
		}
	}
	if len(services) == 0 {
		return printErr("Nothing to update", fmt.Errorf("project %s has no services", project))
	}
	var done []string
	for i, service := range services {
		for _, pair := range pairs {
			if unset {
				err = client.UnsetSecretWithScope(context.Background(), service.Slug, pair.Key, api.PreviewEnvScope)
				// A service that never had the key is already in the
				// desired state.
				var apiErr *api.APIError
				if errors.As(err, &apiErr) && apiErr.Problem.Status == 404 {
					err = nil
				}
			} else {
				err = client.SetSecretWithScope(context.Background(), service.Slug, pair.Key, pair.Value, api.PreviewEnvScope)
			}
			if err != nil {
				var left []string
				for _, other := range services[i+1:] {
					left = append(left, other.Slug)
				}
				detail := fmt.Sprintf("%s: %v", service.Slug, err)
				if len(done) > 0 {
					detail += "; already updated: " + strings.Join(done, ", ")
				}
				if len(left) > 0 {
					detail += "; not updated: " + strings.Join(left, ", ")
				}
				return printErr("Preview secrets stopped", errors.New(detail))
			}
		}
		done = append(done, service.Slug)
	}
	keys := make([]string, len(pairs))
	for i, pair := range pairs {
		keys[i] = pair.Key
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"project": project, "scope": api.PreviewEnvScope, "keys": keys, "services": done, "unset": unset}))
	}
	verb := "Set"
	if unset {
		verb = "Removed"
	}
	PrintOK(osStdout, "%s %s for pull request previews of %d services: %s", verb, strings.Join(keys, ", "), len(done), strings.Join(done, ", "))
	_, _ = fmt.Fprintf(osStdout, "  Open previews receive them at their next deploy; services added later need the command again.\n")
	return 0
}

// previewSecretPairs reads the KEY=VALUE pairs to set, or the keys to unset,
// and validates every key before anything is written.
func previewSecretPairs(unset, fromStdin bool, positional []string) ([]secretsPair, error) {
	var pairs []secretsPair
	switch {
	case unset:
		for _, key := range positional {
			pairs = append(pairs, secretsPair{Key: key})
		}
	case fromStdin:
		if len(positional) != 0 {
			return nil, errors.New("--from-stdin takes no positional pairs")
		}
		scanner := newKeyValueScanner(bufio.NewScanner(osStdin))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			pair, err := parseSecretsPair(line)
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, pair)
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("read stdin: %w", keyValueScanErr(err))
		}
	default:
		for _, arg := range positional {
			pair, err := parseSecretsPair(arg)
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, pair)
		}
	}
	if len(pairs) == 0 {
		return nil, errors.New("at least one key is required")
	}
	for _, pair := range pairs {
		if problem := api.ValidateSecretKey(pair.Key); problem != nil {
			return nil, errors.New(problem.Detail)
		}
	}
	return pairs, nil
}

// adr: 1139
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

const projectSecretsUsage = "usage: gregale projects environments secrets <project-slug> <environment-slug>\n" +
	"       gregale projects environments secrets set <project-slug> <environment-slug> (KEY=VALUE ...|--from-stdin|--file PATH)\n" +
	"       gregale projects environments secrets rotate <project-slug> <environment-slug> (KEY=VALUE ...|--from-stdin|--file PATH)\n" +
	"       gregale projects environments secrets unset <project-slug> <environment-slug> KEY ..."

// cmdProjectsEnvironmentSecrets manages the secrets a project environment
// shares with every service (ADR-1139). Values are write-only.
func cmdProjectsEnvironmentSecrets(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "set":
			return cmdProjectsEnvironmentSecretsWrite(args[1:], false)
		case "rotate":
			return cmdProjectsEnvironmentSecretsWrite(args[1:], true)
		case "unset", "rm":
			return cmdProjectsEnvironmentSecretsUnset(args[1:])
		case "list", "ls":
			args = args[1:]
		}
	}
	if len(args) != 2 || !api.ValidProjectSlug(args[0]) || !api.ValidProjectEnvironmentSlug(args[1]) {
		PrintUsage(os.Stderr, projectSecretsUsage, "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	listed, err := client.ListProjectEnvironmentSecrets(context.Background(), args[0], args[1])
	if err != nil {
		return printErr("Could not list project secrets", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(listed))
	}
	_, _ = fmt.Fprintf(osStdout, "Shared secrets %s/%s (%d of %d)\n", listed.ProjectSlug, listed.Environment, len(listed.Secrets), listed.Limit)
	for _, secret := range listed.Secrets {
		_, _ = fmt.Fprintf(osStdout, "  %s  v%d  updated %s\n", secret.Key, secret.Version, secret.UpdatedAt)
	}
	return 0
}

func cmdProjectsEnvironmentSecretsWrite(args []string, rotate bool) int {
	flags, positional := splitArgsForFlags(args, "from-stdin")
	fs := newFlagSet("projects-environments-secrets-set", flag.ContinueOnError)
	fromStdin := fs.Bool("from-stdin", false, "read KEY=VALUE pairs from stdin (one per line)")
	file := fs.String("file", "", "read KEY=VALUE pairs from a private file")
	if err := fs.Parse(flags); err != nil || len(positional) < 2 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(positional[1]) {
		PrintUsage(os.Stderr, projectSecretsUsage, "projects environments")
		return 1
	}
	pairs, err := projectSecretPairs(positional[2:], *fromStdin, *file)
	if err != nil {
		return printErr("Invalid secret input", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var written []api.ProjectEnvironmentSharedSecretResponse
	for _, pair := range pairs {
		var out api.ProjectEnvironmentSharedSecretResponse
		if rotate {
			out, err = client.RotateProjectEnvironmentSecret(context.Background(), positional[0], positional[1], pair.Key, pair.Value)
		} else {
			out, err = client.SetProjectEnvironmentSecret(context.Background(), positional[0], positional[1], pair.Key, pair.Value)
		}
		if err != nil {
			return printErr("Could not store project secret "+pair.Key, err)
		}
		written = append(written, out)
	}
	if jsonOutput {
		return jsonOut(writeJSON(written))
	}
	for _, out := range written {
		_, _ = fmt.Fprintf(osStdout, "Set %s for %s/%s (v%d). Services receive it at their next boot.\n", out.Secret.Key, out.ProjectSlug, out.Environment, out.Secret.Version)
	}
	return 0
}

func cmdProjectsEnvironmentSecretsUnset(args []string) int {
	if len(args) < 3 || !api.ValidProjectSlug(args[0]) || !api.ValidProjectEnvironmentSlug(args[1]) {
		PrintUsage(os.Stderr, projectSecretsUsage, "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	for _, key := range args[2:] {
		if err := client.DeleteProjectEnvironmentSecret(context.Background(), args[0], args[1], key); err != nil {
			return printErr("Could not remove project secret "+key, err)
		}
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStdout, "Removed %s from %s/%s. Services stop receiving it at their next boot.\n", key, args[0], args[1])
		}
	}
	return 0
}

// projectSecretPairs reads KEY=VALUE pairs from exactly one source: the
// positional arguments, stdin or a private file.
func projectSecretPairs(positional []string, fromStdin bool, file string) ([]secretsPair, error) {
	sources := 0
	for _, used := range []bool{len(positional) > 0, fromStdin, file != ""} {
		if used {
			sources++
		}
	}
	if sources != 1 {
		return nil, errors.New("give KEY=VALUE pairs, --from-stdin or --file, exactly one of them")
	}
	if file != "" {
		return readSecretsFile(file)
	}
	if !fromStdin {
		pairs := make([]secretsPair, 0, len(positional))
		for _, arg := range positional {
			pair, err := parseSecretsPair(arg)
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, pair)
		}
		return pairs, nil
	}
	var pairs []secretsPair
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
	if len(pairs) == 0 {
		return nil, errors.New("stdin held no KEY=VALUE pairs")
	}
	return pairs, nil
}

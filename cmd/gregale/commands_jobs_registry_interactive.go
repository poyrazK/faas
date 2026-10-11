package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/term"
)

func cmdJobsRegistrySetInteractive() int {
	input, ok := osStdin.(*os.File)
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() || !ok || !term.IsTerminal(int(input.Fd())) {
		return printErr("Interactive terminal required", errors.New("use jobs registry set JOB with --password-stdin for scripts"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	job, registry, user, password, confirmed, snapshot, err := collectJobRegistry(ctx, input, client)
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Registry setup canceled.")
		return 0
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	current, err := client.GetJob(readCtx, job.Name)
	if err != nil {
		cancel()
		return printErr("Could not recheck Job", err)
	}
	credentials, err := client.ListJobRegistryCredentials(readCtx, job.Name)
	cancel()
	if err != nil {
		return printErr("Could not recheck credentials", err)
	}
	if current.ID != job.ID || current.AccountID != job.AccountID || current.Name != job.Name || !reflect.DeepEqual(registryCredentialSnapshot(credentials, registry), snapshot) {
		return printErr("Registry target changed", errors.New("run the command again to review current credentials"))
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	response, err := client.SetJobRegistryCredential(writeCtx, job.Name, registry, user, password)
	if err != nil {
		return printErr("Set failed", err)
	}
	PrintOK(osStdout, "Registry credential for %s set (username=%s).", oneLine(response.Registry), oneLine(response.Username))
	return 0
}

func registryCredentialSnapshot(list api.JobRegistryCredentialListResponse, registry string) *api.JobRegistryCredentialResponse {
	for _, credential := range list.Credentials {
		normalized, err := registryInputForAPI(credential.Registry)
		if err == nil && normalized == registry {
			copy := credential
			copy.LastUsedAt = ""
			return &copy
		}
	}
	return nil
}

// One raw terminal reader owns all prompts, preventing visible reads from
// buffering a following token. Echo is restored before the write request.
func collectJobRegistry(ctx context.Context, input *os.File, client *api.Client) (api.JobResponse, string, string, string, bool, *api.JobRegistryCredentialResponse, error) {
	var job api.JobResponse
	fail := func(err error) (api.JobResponse, string, string, string, bool, *api.JobRegistryCredentialResponse, error) {
		return job, "", "", "", false, nil, err
	}
	state, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return fail(err)
	}
	defer func() { _ = term.Restore(int(input.Fd()), state) }()
	stream := secretTerminalIO{input: input, output: osStderr}
	terminal := &secretEntryTerminal{Terminal: term.NewTerminal(stream, ""), input: stream, maxBytes: api.MaxRegistryPasswordBytes}
	offset := 0
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		page, err := client.ListJobs(readCtx, 20, offset)
		cancel()
		if err != nil {
			return fail(err)
		}
		for i, item := range page.Jobs {
			if !jobSlugPattern.MatchString(item.Name) || !jobRunIDPattern.MatchString(item.ID) {
				return fail(errors.New("invalid Job identity"))
			}
			_, _ = fmt.Fprintf(terminal, "%d. %s\n", i+1, oneLine(item.Name))
		}
		_, _ = fmt.Fprintln(terminal, "Choose a number; n shows more; q cancels.")
		value, err := secretTerminalRead(ctx, terminal, "Job", false)
		if err != nil {
			return fail(err)
		}
		if value == "q" {
			return fail(nil)
		}
		if value == "n" && page.NextOffset > offset {
			offset = page.NextOffset
			continue
		}
		choice, err := strconv.Atoi(value)
		if err != nil || choice < 1 || choice > len(page.Jobs) {
			_, _ = fmt.Fprintln(terminal, "Choose a listed Job.")
			continue
		}
		job = page.Jobs[choice-1]
		break
	}
	if job.Name == "" {
		return fail(errors.New("no Job selected within the page limit"))
	}
	registry := ""
	for {
		value, err := secretTerminalRead(ctx, terminal, "Registry host[:port]", false)
		if err != nil {
			return fail(err)
		}
		registry, err = registryInputForAPI(value)
		if err == nil {
			break
		}
		_, _ = fmt.Fprintln(terminal, "Enter a valid lowercase registry host[:port].")
	}
	user := ""
	for {
		user, err = secretTerminalRead(ctx, terminal, "Username", false)
		if err != nil {
			return fail(err)
		}
		if user != "" && len(user) <= api.MaxRegistryUsernameLen && strings.IndexFunc(user, unicode.IsControl) < 0 {
			break
		}
		_, _ = fmt.Fprintln(terminal, "Enter a nonempty username within the supported limit.")
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	credentials, err := client.ListJobRegistryCredentials(readCtx, job.Name)
	cancel()
	if err != nil {
		return fail(err)
	}
	snapshot := registryCredentialSnapshot(credentials, registry)
	action := "Add"
	if snapshot != nil {
		action = "Replace"
	} else if credentials.Count >= credentials.QuotaMax {
		return fail(errors.New("registry credential quota is full"))
	}
	password, err := secretTerminalRead(ctx, terminal, "Password/token (hidden)", true)
	if err != nil {
		return fail(err)
	}
	if password == "" || strings.IndexFunc(password, unicode.IsControl) >= 0 {
		return fail(errors.New("enter a nonempty password/token without control characters"))
	}
	_, _ = fmt.Fprintf(terminal, "Review: %s credential for Job %s; registry=%s; username=%s; token=hidden\n", action, job.Name, registry, user)
	if snapshot != nil {
		_, _ = fmt.Fprintf(terminal, "Existing username: %s\n", oneLine(snapshot.Username))
	}
	answer, err := secretTerminalRead(ctx, terminal, "Save credential? (y/N)", false)
	if err != nil {
		return fail(err)
	}
	return job, registry, user, password, strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes"), snapshot, nil
}

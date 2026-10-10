package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"sort"
	"syscall"
	"time"
)

func cmdJobsRegistryRmInteractive() int {
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use jobs registry rm JOB --registry HOST for scripts"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	job, selected, err := chooseJob(ctx, client, prompt)
	if err != nil {
		return jobLogPickerError(err)
	}
	if !selected {
		return 0
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	list, err := client.ListJobRegistryCredentials(readCtx, job.Name)
	cancel()
	if err != nil {
		return printErr("Could not list credentials", err)
	}
	if len(list.Credentials) == 0 {
		PrintProgress(osStdout, "No registry credentials for %s.", job.Name)
		return 0
	}
	sort.Slice(list.Credentials, func(i, j int) bool { return list.Credentials[i].Registry < list.Credentials[j].Registry })
	labels := []string{}
	seen := map[string]bool{}
	for _, credential := range list.Credentials {
		registry, err := registryInputForAPI(credential.Registry)
		if err != nil || seen[registry] {
			return printErr("Invalid credential list", errors.New("a registry host is invalid or duplicated"))
		}
		seen[registry] = true
		labels = append(labels, fmt.Sprintf("%s · username %s · last used %s", oneLine(credential.Registry), oneLine(credential.Username), oneLine(credential.LastUsedAt)))
	}
	labels = append(labels, "Cancel")
	choice, err := prompt.choose(ctx, "Choose a registry credential to remove for "+job.Name+".", labels, 0)
	if err != nil {
		return startInputExit(err)
	}
	if choice == len(list.Credentials) {
		return 0
	}
	credential := list.Credentials[choice]
	registry, err := registryInputForAPI(credential.Registry)
	if err != nil {
		return printErr("Invalid registry", err)
	}
	snapshot := registryCredentialSnapshot(list, registry)
	PrintProgress(osStdout, "Remove credential: Job=%s; registry=%s; username=%s\nCreated: %s; updated: %s; last used: %s", job.Name, oneLine(credential.Registry), oneLine(credential.Username), oneLine(credential.CreatedAt), oneLine(credential.UpdatedAt), oneLine(credential.LastUsedAt))
	PrintProgress(osStdout, "Future private image pulls using this credential may fail until a credential is set again.")
	confirmed, err := prompt.confirm(ctx, "Remove this registry credential?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Credential removal canceled.")
		return 0
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	current, err := client.GetJob(readCtx, job.Name)
	if err != nil {
		cancel()
		return printErr("Could not recheck Job", err)
	}
	latest, err := client.ListJobRegistryCredentials(readCtx, job.Name)
	cancel()
	if err != nil {
		return printErr("Could not recheck credentials", err)
	}
	if current.ID != job.ID || current.Name != job.Name || current.AccountID != job.AccountID {
		return printErr("Job changed", errors.New("run the command again to review the current Job"))
	}
	currentCredential := registryCredentialSnapshot(latest, registry)
	if currentCredential == nil {
		PrintProgress(osStdout, "Credential already removed; no deletion submitted.")
		return 0
	}
	if !reflect.DeepEqual(currentCredential, snapshot) {
		return printErr("Credential changed", errors.New("run the command again to review the current credential"))
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := client.DeleteJobRegistryCredential(writeCtx, job.Name, registry); err != nil {
		return printErr("Delete failed", err)
	}
	PrintOK(osStdout, "Registry credential for %s removed from %s.", oneLine(credential.Registry), job.Name)
	return 0
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runrelease"
)

// This dispatch precedes wire.Daemon. No subcommand starts services, opens the
// database or calls native recovery. Older binaries are never invoked by it.
func runReleaseCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "use vmmd run-release capabilities|prepare|preflight|rollback [--plan PATH]")
		return adoptionExitError
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RunReleaseCheckTimeout)
	defer cancel()
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run-release: cannot locate executing binary")
		return adoptionExitError
	}
	digest, err := runrelease.Digest(ctx, exe)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run-release: cannot hash executing binary")
		return adoptionExitError
	}
	if args[0] == "capabilities" {
		if len(args) != 1 {
			return adoptionExitError
		}
		if err := json.NewEncoder(stdout).Encode(runrelease.Capabilities(digest, runtime.GOOS, runtime.GOARCH)); err != nil {
			return adoptionExitError
		}
		return adoptionExitOK
	}
	if args[0] == "prepare" {
		return prepareRunReleasePlan(ctx, args[1:], exe, stdout, stderr)
	}
	if args[0] != "preflight" && args[0] != "rollback" {
		_, _ = fmt.Fprintln(stderr, "run-release: unknown action")
		return adoptionExitError
	}
	flags := flag.NewFlagSet("run-release "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	planPath := flags.String("plan", "", "reviewed release plan with absolute hash-pinned inputs")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *planPath == "" {
		return adoptionExitError
	}
	body, err := runrelease.Read(*planPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run-release: cannot read plan")
		return adoptionExitError
	}
	var plan runrelease.Plan
	if err := runrelease.Decode(body, &plan); err != nil {
		_, _ = fmt.Fprintln(stderr, "run-release: invalid plan JSON")
		return adoptionExitError
	}
	candidate, candidateErr := loadRunReleaseConfig(ctx, plan.CandidateConfig)
	rollback, rollbackErr := loadRunReleaseConfig(ctx, plan.RollbackConfig)
	if err := errors.Join(candidateErr, rollbackErr); err != nil {
		// Config files can contain secrets. Neither parser errors nor config
		// contents belong in the portable release result.
		_, _ = fmt.Fprintln(stderr, "run-release: configuration could not be verified or parsed; inspect it privately")
		return adoptionExitError
	}
	result := runrelease.Evaluate(ctx, args[0], runrelease.Inputs{
		Plan: plan, PlanSHA256: runrelease.SHA256(body), Candidate: candidate, Rollback: rollback,
		Host: runrelease.ProbeHost(ctx), ExecutingBinarySHA256: digest,
		NativeJournalRoot: filepath.Join(fcvm.JailChrootBase, ".native-processes"),
	})
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return adoptionExitError
	}
	if !result.Passed {
		return adoptionExitNotYet
	}
	return adoptionExitOK
}

func loadRunReleaseConfig(ctx context.Context, pin runrelease.File) (runrelease.Config, error) {
	body, err := runrelease.ReadPinned(ctx, pin)
	if err != nil {
		return runrelease.Config{}, err
	}
	c, err := loadConfig(pin.Path, func(string) ([]byte, error) { return body, nil })
	if err != nil {
		return runrelease.Config{}, err
	}
	return runrelease.Config{
		NativeRecovery: c.NativeProcessRecovery, LaunchOwners: c.NativeLaunchOwnerRecords, RuntimeCapture: c.NativeRuntimeCapture,
		QualificationCapture: c.NativeQualificationCapture, AdoptionRecord: c.AdoptionRecord, AdoptRunning: c.AdoptRunningVMs,
		PreparedNetworks: c.PreparedNetworks, PublicationRoot: c.NativeSnapshotPublicationRoot, ImageRoot: c.NativeImageStagingRoot,
		SnapshotRoot: c.NativeRuntimeSnapshotRoot, ResourceRoot: c.ResourceJournalDir, VolatileRoot: c.ResourceJournalVolatileDir, VolatileWrites: c.ResourceJournalVolatileWrites,
	}, nil
}

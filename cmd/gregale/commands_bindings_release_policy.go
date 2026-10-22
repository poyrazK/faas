package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdBindingsReleasePolicy(args []string) int {
	if len(args) == 0 || args[0] != "get" && args[0] != "set" {
		return printErr("Invalid release policy command", errors.New("usage: gregale bindings release-policy <get|set> <app> --scope SCOPE"))
	}
	action := args[0]
	fs := newFlagSet("bindings release-policy "+action, flag.ContinueOnError)
	scope := fs.String("scope", "default", "deployment scope")
	var r api.SetBindingReleasePolicyRequest
	var revision int64
	var requireVerification bool
	var storageRequirements storageRequirementsFlag
	var clearStorageRequirements bool
	var queueRequirements queueRequirementsFlag
	var clearQueueRequirements bool
	if action == "set" {
		fs.StringVar(&r.Mode, "mode", "", "off or enforce")
		fs.BoolVar(&requireVerification, "require-verification", false, "enable verification enforcement (alias for --mode enforce)")
		fs.StringVar(&r.MaxVerificationAge, "max-age", "10m", "maximum verification age (1s to 24h)")
		fs.BoolVar(&r.RequireApplicationAck, "require-application-ack", false, "require current application acknowledgements")
		fs.Var(&storageRequirements, "object-storage-require", "require PREFIX=read|write|read_write (repeatable; replaces all storage requirements)")
		fs.BoolVar(&clearStorageRequirements, "clear-object-storage-requirements", false, "explicitly remove all storage operation requirements")
		fs.Var(&queueRequirements, "queue-require", "require NAME=consumer_readiness or NAME=handler_completion (repeatable; replaces all queue requirements)")
		fs.BoolVar(&clearQueueRequirements, "clear-queue-requirements", false, "explicitly remove all queue readiness requirements")
		fs.Int64Var(&revision, "expected-revision", -1, "current policy revision; use 0 initially")
		fs.StringVar(&r.Reason, "reason", "", "reason for the update; required when disabling enforcement")
	}
	flags, positional := splitArgsForFlags(args[1:], "require-verification", "require-application-ack", "clear-object-storage-requirements", "clear-queue-requirements")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) || api.ValidateScope(*scope) != nil {
		return printErr("Invalid release policy command", errors.New("supply an app slug and a valid scope"))
	}
	if action == "set" {
		if clearStorageRequirements && len(storageRequirements) > 0 {
			return printErr("Invalid release policy command", errors.New("cannot require storage operations and clear them in the same update"))
		}
		if clearQueueRequirements && len(queueRequirements) > 0 {
			return printErr("Invalid release policy command", errors.New("cannot require queue readiness and clear it in the same update"))
		}
		r.QueueRequirements = queueRequirements
		if clearQueueRequirements {
			r.QueueRequirements = map[string]string{}
		}
		r.ObjectStorageRequirements = storageRequirements
		if clearStorageRequirements {
			r.ObjectStorageRequirements = map[string]string{}
		}
		if requireVerification {
			if r.Mode != "" && r.Mode != "enforce" {
				return printErr("Invalid release policy command", errors.New("--require-verification conflicts with --mode off"))
			}
			r.Mode = "enforce"
		}
		r.ExpectedRevision = &revision
		if err := api.ValidateBindingReleasePolicyRequest(r); err != nil {
			return printErr("Invalid release policy command", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RouteCheckTimeout)
	defer cancel()
	var p api.BindingReleasePolicy
	if action == "set" {
		p, err = client.SetBindingReleasePolicy(ctx, positional[0], *scope, r)
	} else {
		p, err = client.GetBindingReleasePolicy(ctx, positional[0], *scope)
	}
	if err != nil {
		return printBindingPromotionError(err)
	}
	if err := validateBindingReleasePolicyResult(p, *scope); err != nil {
		return printErr("Invalid release policy response", err)
	}
	if action == "set" {
		age, _ := time.ParseDuration(r.MaxVerificationAge)
		actual, _ := time.ParseDuration(p.MaxVerificationAge)
		if p.Mode != r.Mode || p.Revision != revision+1 || p.RequireApplicationAck != r.RequireApplicationAck || actual != age || p.Reason != r.Reason || !maps.Equal(p.ObjectStorageRequirements, r.ObjectStorageRequirements) || !maps.Equal(p.QueueRequirements, r.QueueRequirements) {
			return printErr("Invalid release policy response", errors.New("server did not confirm the submitted policy and revision"))
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(p))
	}
	_, _ = fmt.Fprintf(osStdout, "Binding release policy for %s (%s)\nMode: %s\nRevision: %d\nMaximum verification age: %s\nRequire application ACK: %t\n", positional[0], p.Scope, p.Mode, p.Revision, p.MaxVerificationAge, p.RequireApplicationAck)
	if p.Reason != "" {
		_, _ = fmt.Fprintf(osStdout, "Reason: %s\n", p.Reason)
	}
	if len(p.ObjectStorageRequirements) > 0 {
		requirements := storageRequirementsFlag(p.ObjectStorageRequirements)
		_, _ = fmt.Fprintf(osStdout, "Object-storage requirements: %s\n", requirements.String())
	}
	if len(p.QueueRequirements) > 0 {
		requirements := queueRequirementsFlag(p.QueueRequirements)
		_, _ = fmt.Fprintf(osStdout, "Queue requirements: %s (scheduler readiness; connectivity remains unverified)\n", requirements.String())
	}
	return 0
}

func validateBindingReleasePolicyResult(p api.BindingReleasePolicy, scope string) error {
	if err := api.ValidateObjectStorageRequirements(p.ObjectStorageRequirements); err != nil {
		return err
	}
	if err := api.ValidateQueueReleaseRequirements(p.QueueRequirements); err != nil {
		return err
	}
	id, err := uuid.Parse(p.AppID)
	age, ageErr := time.ParseDuration(p.MaxVerificationAge)
	if err != nil || id == uuid.Nil || p.Scope != scope || p.Mode != "off" && p.Mode != "enforce" || p.Revision < 0 || p.Revision > api.BindingReleasePolicyMaxRevision || p.Revision == 0 && p.Mode != "off" || p.Revision > 0 && p.UpdatedAt == nil || ageErr != nil || age < time.Second || age > api.BindingReleasePolicyMaxAge {
		return errors.New("release policy identity, scope, mode or revision is invalid")
	}
	return nil
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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
	if action == "set" {
		fs.StringVar(&r.Mode, "mode", "", "off or enforce")
		fs.BoolVar(&requireVerification, "require-verification", false, "enable verification enforcement (alias for --mode enforce)")
		fs.StringVar(&r.MaxVerificationAge, "max-age", "10m", "maximum verification age (1s to 24h)")
		fs.BoolVar(&r.RequireApplicationAck, "require-application-ack", false, "require current application acknowledgements")
		fs.Int64Var(&revision, "expected-revision", -1, "current policy revision; use 0 initially")
		fs.StringVar(&r.Reason, "reason", "", "reason for the update; required when disabling enforcement")
	}
	flags, positional := splitArgsForFlags(args[1:], "require-verification", "require-application-ack")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) || api.ValidateScope(*scope) != nil {
		return printErr("Invalid release policy command", errors.New("supply an app slug and a valid scope"))
	}
	if action == "set" {
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
		if p.Mode != r.Mode || p.Revision != revision+1 || p.RequireApplicationAck != r.RequireApplicationAck || actual != age || p.Reason != r.Reason {
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
	return 0
}

func validateBindingReleasePolicyResult(p api.BindingReleasePolicy, scope string) error {
	id, err := uuid.Parse(p.AppID)
	age, ageErr := time.ParseDuration(p.MaxVerificationAge)
	if err != nil || id == uuid.Nil || p.Scope != scope || p.Mode != "off" && p.Mode != "enforce" || p.Revision < 0 || p.Revision > api.BindingReleasePolicyMaxRevision || p.Revision == 0 && p.Mode != "off" || p.Revision > 0 && p.UpdatedAt == nil || ageErr != nil || age < time.Second || age > api.BindingReleasePolicyMaxAge {
		return errors.New("release policy identity, scope, mode or revision is invalid")
	}
	return nil
}

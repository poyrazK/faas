// adr: 1302
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storedworkflowgate"
)

type storedWorkflowLiveBaselineOptions struct {
	verb, app, name, alias, promotionID string
	retirementID                        string
	confirmRetirement                   bool
	request                             api.PromoteStoredWorkflowLiveBaselineRequest
}

func parseStoredWorkflowLiveBaseline(args []string) (storedWorkflowLiveBaselineOptions, error) {
	var o storedWorkflowLiveBaselineOptions
	if len(args) == 0 {
		return o, errors.New("choose baseline list, show, history, promote or retire")
	}
	o.verb = args[0]
	if o.verb != "list" && o.verb != "show" && o.verb != "history" && o.verb != "promote" && o.verb != "retire" {
		return o, errors.New("choose baseline list, show, history, promote or retire")
	}
	fs := newFlagSet("workflow stored live baseline "+o.verb, flag.ContinueOnError, io.Discard)
	fs.StringVar(&o.app, "app", "", "app slug")
	fs.StringVar(&o.name, "name", "", "stored workflow name")
	if o.verb != "list" {
		fs.StringVar(&o.alias, "baseline-name", "", "named release track, such as production")
	}
	if o.verb == "retire" {
		fs.StringVar(&o.retirementID, "retirement-id", "", "stable retirement UUID; preserve for retries")
		fs.Int64Var(&o.request.ExpectedRevision, "expected-revision", 0, "current positive baseline revision")
		fs.BoolVar(&o.confirmRetirement, "confirm-retirement", false, "confirm permanent name retirement")
	}
	var policyPath string
	if o.verb == "promote" {
		fs.StringVar(&o.promotionID, "promotion-id", "", "stable promotion UUID; preserve for retries")
		fs.StringVar(&o.request.SuiteID, "suite", "", "passing retained suite UUID")
		fs.Int64Var(&o.request.ExpectedRevision, "expected-revision", 0, "current baseline revision; explicit 0 creates")
		fs.Int64Var(&o.request.ExpectedVersion, "expected-version", 0, "expected saved version")
		fs.Int64Var(&o.request.ExpectedPublishedVersion, "expected-published-version", 0, "expected published revision")
		fs.StringVar(&o.request.ExpectedDeploymentID, "expected-deployment-id", "", "expected deployment UUID")
		fs.StringVar(&policyPath, "policy", "", "version 2 coverage and age policy JSON file")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return o, errors.New("could not parse baseline flags; use workflow stored live baseline --help")
	}
	if fs.NArg() != 0 || !validCLISlug(o.app) || (o.verb != "list" && !api.ValidStoredWorkflowLiveBaselineName(o.alias)) || o.name == "" {
		return o, errors.New("provide --app and --name, plus a valid --baseline-name for show, history, promote or retire; no positional arguments")
	}
	if o.verb == "retire" {
		id, err := uuid.Parse(o.retirementID)
		if err != nil || id == uuid.Nil || o.request.ExpectedRevision <= 0 || o.request.ExpectedRevision == math.MaxInt64 || !o.confirmRetirement {
			return o, errors.New("provide a retirement UUID, positive --expected-revision and --confirm-retirement")
		}
		o.retirementID = id.String()
		return o, nil
	}
	if o.verb != "promote" {
		return o, nil
	}
	revisionSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "expected-revision" {
			revisionSet = true
		}
	})
	selection := storedworkflowgate.Selection{Name: o.name, CandidateID: o.request.SuiteID, Expected: storedworkflowgate.Target{Version: o.request.ExpectedVersion, PublishedVersion: o.request.ExpectedPublishedVersion, DeploymentID: o.request.ExpectedDeploymentID}}
	id, err := uuid.Parse(o.promotionID)
	if !revisionSet || o.request.ExpectedRevision < 0 || o.request.ExpectedRevision == math.MaxInt64 || err != nil || id == uuid.Nil || selection.ValidateCandidate() != nil || policyPath == "" {
		return o, errors.New("provide a promotion UUID, suite UUID, explicit expected baseline revision and complete expected candidate target")
	}
	policy, err := readStoredWorkflowGatePolicy(policyPath)
	if err != nil || policy.Version != 2 {
		return o, errors.New("promotion requires a valid version 2 coverage and age policy file")
	}
	o.promotionID = id.String()
	o.request.SuiteID = uuid.MustParse(o.request.SuiteID).String()
	o.request.ExpectedDeploymentID = uuid.MustParse(o.request.ExpectedDeploymentID).String()
	o.request.Policy, _ = json.Marshal(policy)
	if int64(len(o.request.Policy)) > api.StoredWorkflowLiveGatePolicyMaxBytes {
		return o, fmt.Errorf("canonical promotion policy has %d bytes; maximum %d", len(o.request.Policy), api.StoredWorkflowLiveGatePolicyMaxBytes)
	}
	return o, nil
}
func cmdStoredWorkflowsLiveBaseline(args []string) int {
	o, err := parseStoredWorkflowLiveBaseline(args)
	if err != nil {
		return printErr("Invalid baseline options", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, api.StoredWorkflowLiveProgressRequestTimeout)
	defer cancel()
	if o.verb == "retire" {
		return runStoredWorkflowLiveBaselineRetire(ctx, client, o)
	}
	if o.verb == "list" {
		return runStoredWorkflowLiveBaselineList(ctx, client, o)
	}
	if o.verb == "history" {
		view, err := client.GetStoredWorkflowLiveBaselineHistory(ctx, o.app, o.name, o.alias)
		if err != nil {
			return printErr("Could not read baseline history", err)
		}
		if len(view.Promotions) == 0 || len(view.Promotions) > api.StoredWorkflowLiveBaselineHistoryLimit {
			return invalidStoredWorkflowLiveBaselineReceipt()
		}
		if view.Retirement != nil && (!validBaselineRetirement(*view.Retirement, o.name, o.alias) || view.Retirement.AppID != view.Promotions[0].AppID || view.Retirement.Revision != view.Promotions[0].Revision+1 || view.Retirement.PreviousPromotionID != view.Promotions[0].PromotionID) {
			return invalidStoredWorkflowLiveBaselineReceipt()
		}
		for i, receipt := range view.Promotions {
			if _, err := validateStoredWorkflowLiveBaselineReceipt(receipt, o.name, o.alias); err != nil {
				return invalidStoredWorkflowLiveBaselineReceipt()
			}
			if i > 0 && (receipt.Revision != view.Promotions[i-1].Revision-1 || receipt.AppID != view.Promotions[0].AppID || view.Promotions[i-1].PreviousPromotionID != receipt.PromotionID) {
				return invalidStoredWorkflowLiveBaselineReceipt()
			}
		}
		if jsonOutput {
			if err := writeJSON(view); err != nil {
				return printErr("Could not write baseline history", err)
			}
			return 0
		}
		if view.Retirement != nil {
			if err := writeStoredWorkflowLiveBaselineRetirementText(*view.Retirement); err != nil {
				return printErr("Could not write retirement history", err)
			}
		}
		for _, receipt := range view.Promotions {
			if err := writeStoredWorkflowLiveBaselineText(receipt); err != nil {
				return printErr("Could not write baseline history", err)
			}
		}
		return 0
	}
	var receipt api.StoredWorkflowLiveBaseline
	if o.verb == "promote" {
		receipt, err = client.PromoteStoredWorkflowLiveBaseline(ctx, o.app, o.name, o.alias, o.promotionID, o.request)
	} else {
		receipt, err = client.GetStoredWorkflowLiveBaseline(ctx, o.app, o.name, o.alias)
	}
	if err != nil {
		return printErr("Could not read or promote baseline; retry uncertain promotions only with the original ID, revision, target and policy", err)
	}
	policy, validateErr := validateStoredWorkflowLiveBaselineReceipt(receipt, o.name, o.alias)
	if validateErr != nil {
		return invalidStoredWorkflowLiveBaselineReceipt()
	}
	if o.verb == "promote" {
		requestedPolicy, _ := storedworkflowgate.ParseCoveragePolicy(o.request.Policy)
		if receipt.PromotionID != o.promotionID || receipt.Suite.ID != o.request.SuiteID || receipt.Revision != o.request.ExpectedRevision+1 || receipt.Suite.Version != o.request.ExpectedVersion || receipt.Suite.PublishedVersion != o.request.ExpectedPublishedVersion || receipt.Suite.DeploymentID != o.request.ExpectedDeploymentID || policy.Hash() != requestedPolicy.Hash() {
			return invalidStoredWorkflowLiveBaselineReceipt()
		}
	}
	if jsonOutput {
		err = writeJSON(receipt)
	} else {
		err = writeStoredWorkflowLiveBaselineText(receipt)
	}
	if err != nil {
		return printErr("Could not write baseline receipt", err)
	}
	return 0
}
func writeStoredWorkflowLiveBaselineText(receipt api.StoredWorkflowLiveBaseline) error {
	_, err := fmt.Fprintf(osStdout, "Baseline %s revision %d: suite %s\nPromotion: %s at %s by account %s\nPolicy SHA-256: %s\n", receipt.BaselineName, receipt.Revision, receipt.Suite.ID, receipt.PromotionID, receipt.PromotedAt.Format("2006-01-02T15:04:05Z07:00"), receipt.ActorAccountID, receipt.PolicyHash)
	return err
}

func invalidStoredWorkflowLiveBaselineReceipt() int {
	return printErr("Invalid baseline receipt", &exitErr{msg: "The server response did not contain consistent evidence for the selected baseline or promotion. No confirmed promotion receipt is available; preserve the original promotion ID for retries.", code: 3})
}

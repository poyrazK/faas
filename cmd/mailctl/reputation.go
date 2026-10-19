package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func reputationCLIRoute(route string) bool {
	return route == internalmail.ReputationPath || strings.HasPrefix(route, internalmail.ReputationPath+"/") || strings.HasPrefix(route, internalmail.ReputationPath+"?")
}
func parseReputationCommand(args []string, diagnostics io.Writer) (command, error) {
	key := args[1]
	fs := flag.NewFlagSet("mailctl reputation "+key, flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	var domainID, producer, after string
	var legacy, reviewed bool
	var audit internalmail.OperatorAuditInput
	limit := api.InternalMailOperatorPageSize
	expected := int64(-1)
	switch key {
	case "holds", "events":
		addPageFlags(fs, &limit, &after)
	case "status", "release":
		fs.StringVar(&domainID, "domain-id", "", "sending domain UUID")
		fs.StringVar(&producer, "producer", "", "stable producer ID")
		fs.BoolVar(&legacy, "legacy", false, "unscoped platform sender")
		if key == "release" {
			fs.Int64Var(&expected, "expected-revision", -1, "hold revision from reputation status")
			fs.BoolVar(&reviewed, "confirmed-reviewed", false, "operator reviewed the cause and evidence")
			addAuditFlags(fs, &audit)
		}
	default:
		return command{}, fmt.Errorf("unknown reputation command; use mailctl help")
	}
	if err := fs.Parse(args[2:]); err != nil {
		return command{}, err
	}
	if fs.NArg() != 0 {
		return command{}, fmt.Errorf("positional arguments are not accepted")
	}
	if key == "holds" || key == "events" {
		if limit < 1 || limit > api.InternalMailMaxOperatorPageSize || len(after) > api.InternalMailMaxOperatorCursorBytes {
			return command{}, fmt.Errorf("invalid page bounds")
		}
		if after != "" && key == "events" && !canonicalCLIUUID(after) {
			return command{}, fmt.Errorf("invalid event cursor")
		}
		if after != "" && key == "holds" {
			kind, value, ok := strings.Cut(after, ":")
			if !ok || (internalmail.ReputationScope{Kind: kind, Key: value}).Validate() != nil {
				return command{}, fmt.Errorf("invalid hold cursor")
			}
		}
		query := url.Values{"limit": {strconv.Itoa(limit)}}
		if after != "" {
			query.Set("after", after)
		}
		return command{http.MethodGet, internalmail.ReputationPath + "/" + key + "?" + query.Encode(), nil}, nil
	}
	scope, err := reputationCLIScope(domainID, producer, legacy)
	if err != nil {
		return command{}, err
	}
	if key == "status" {
		return command{http.MethodGet, internalmail.ReputationPath + "?" + url.Values{"scope": {scope.Kind}, "key": {scope.Key}}.Encode(), nil}, nil
	}
	input := internalmail.ReputationReleaseRequest{OperatorAuditInput: audit, ReputationScope: scope, ExpectedRevision: &expected, ConfirmedReviewed: reviewed}
	if input.Validate() != nil {
		return command{}, fmt.Errorf("release requires a positive revision, --confirmed-reviewed and all audit flags")
	}
	return command{http.MethodPost, internalmail.ReputationPath + "/release", input}, nil
}
func reputationCLIScope(domainID, producer string, legacy bool) (internalmail.ReputationScope, error) {
	selected := 0
	for _, on := range []bool{domainID != "", producer != "", legacy} {
		if on {
			selected++
		}
	}
	scope := internalmail.ReputationScope{Kind: "producer", Key: producer}
	if domainID != "" {
		scope = internalmail.ReputationScope{Kind: "domain", Key: domainID}
	}
	if selected != 1 || scope.Validate() != nil {
		return internalmail.ReputationScope{}, fmt.Errorf("choose exactly one of --domain-id UUID, --producer ID or --legacy")
	}
	return scope, nil
}

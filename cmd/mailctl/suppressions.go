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

func suppressionCLIRoute(route string) bool {
	return strings.HasPrefix(route, internalmail.SuppressionControlsPath+"/")
}

func parseSuppressionCommand(args []string, diagnostics io.Writer) (command, error) {
	fs := flag.NewFlagSet("mailctl suppressions "+args[1], flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	input := internalmail.SuppressionRequest{}
	revision, limit, after := int64(-1), api.InternalMailOperatorPageSize, ""
	fs.StringVar(&input.Address, "address", "", "exact bare mailbox; namespace policy is case insensitive")
	switch args[1] {
	case "show":
	case "history":
		addPageFlags(fs, &limit, &after)
	case "add", "release":
		fs.Int64Var(&revision, "expected-revision", -1, "revision from suppressions show")
		addAuditFlags(fs, &input.OperatorAuditInput)
		if args[1] == "release" {
			fs.BoolVar(&input.ConfirmedReviewed, "confirmed-reviewed", false, "address and block evidence reviewed before release")
		}
	default:
		return command{}, fmt.Errorf("unknown suppressions command; use mailctl help")
	}
	if err := fs.Parse(args[2:]); err != nil {
		return command{}, err
	}
	if fs.NArg() != 0 {
		return command{}, fmt.Errorf("positional arguments are not accepted")
	}
	address, err := internalmail.NormalizeSuppressionAddress(input.Address)
	if err != nil {
		return command{}, fmt.Errorf("a valid exact --address is required")
	}
	input.Address = address
	query := url.Values{"address": {address}}
	if args[1] == "show" {
		return command{http.MethodGet, internalmail.SuppressionControlsPath + "/status?" + query.Encode(), nil}, nil
	}
	if args[1] == "history" {
		if limit < 1 || limit > api.InternalMailMaxOperatorPageSize {
			return command{}, fmt.Errorf("invalid history page size")
		}
		if after != "" {
			n, err := strconv.ParseInt(after, 10, 64)
			if err != nil || n < 1 || strconv.FormatInt(n, 10) != after {
				return command{}, fmt.Errorf("history cursor must be a positive revision")
			}
			query.Set("after", after)
		}
		query.Set("limit", strconv.Itoa(limit))
		return command{http.MethodGet, internalmail.SuppressionControlsPath + "/history?" + query.Encode(), nil}, nil
	}
	input.ExpectedRevision = &revision
	if input.Validate(args[1]) != nil {
		return command{}, fmt.Errorf("suppression mutation requires a current revision and all audit flags; release also requires --confirmed-reviewed")
	}
	return command{http.MethodPost, internalmail.SuppressionControlsPath + "/" + args[1], input}, nil
}

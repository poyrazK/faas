package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func producerLifecycleCLIRoute(route string) bool {
	return route == internalmail.ProducerLifecyclePath || strings.HasPrefix(route, internalmail.ProducerLifecyclePath+"/")
}

func parseProducerLifecycleCommand(args []string, diagnostics io.Writer) (command, error) {
	fs := flag.NewFlagSet("mailctl producers "+args[1], flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	input := internalmail.ProducerLifecycleRequest{}
	revision := int64(-1)
	overlap := api.InternalMailCredentialOverlap
	fs.StringVar(&input.ProducerID, "producer", "", "stable producer ID")
	if args[1] != "status" {
		fs.Int64Var(&revision, "expected-revision", -1, "revision from producers status")
		addAuditFlags(fs, &input.OperatorAuditInput)
	}
	switch args[1] {
	case "status", "suspend":
	case "resume":
		fs.BoolVar(&input.ConfirmedReviewed, "confirmed-reviewed", false, "producer reviewed before resuming")
	case "rotate":
		fs.StringVar(&input.CredentialID, "replacement-id", "", "replacement credential ID from status")
		fs.DurationVar(&overlap, "overlap", overlap, "old credential overlap, 0..24h in whole seconds")
	case "revoke":
		fs.StringVar(&input.CredentialID, "credential-id", "", "credential ID from status or saved audit")
	default:
		return command{}, fmt.Errorf("unknown producers command; use mailctl help")
	}
	if err := fs.Parse(args[2:]); err != nil {
		return command{}, err
	}
	if fs.NArg() != 0 {
		return command{}, fmt.Errorf("positional arguments are not accepted")
	}
	if _, err := internalmail.ProducerMessagesRoute(input.ProducerID); err != nil {
		return command{}, fmt.Errorf("valid --producer is required")
	}
	if args[1] == "status" {
		return command{http.MethodGet, internalmail.ProducerLifecyclePath + "/" + input.ProducerID, nil}, nil
	}
	input.ExpectedRevision = &revision
	if args[1] == "rotate" {
		if overlap < 0 || overlap > api.InternalMailMaxCredentialOverlap || overlap%time.Second != 0 {
			return command{}, fmt.Errorf("overlap must be whole seconds in 0..24h")
		}
		seconds := int32(overlap / time.Second)
		input.OverlapSeconds = &seconds
	}
	if input.Validate(args[1]) != nil {
		return command{}, fmt.Errorf("producer mutation requires a current revision, valid credential selection and all audit flags; resume also requires --confirmed-reviewed")
	}
	return command{http.MethodPost, internalmail.ProducerLifecyclePath + "/" + args[1], input}, nil
}

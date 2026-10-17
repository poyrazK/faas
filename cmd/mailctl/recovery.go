package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func recoveryCLIRoute(route string) bool {
	return route == internalmail.RecoveryPath || strings.HasPrefix(route, internalmail.RecoveryPath+"/")
}

func parseRecoveryCommand(args []string, diagnostics io.Writer) (command, error) {
	fs := flag.NewFlagSet("mailctl recovery "+args[1], flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	var audit internalmail.OperatorAuditInput
	var id, recoveryID, after string
	revision, limit := int64(-1), api.InternalMailOperatorPageSize
	review := internalmail.RecoveryReviewRequest{}
	release := internalmail.RecoveryReleaseRequest{}
	switch args[1] {
	case "status":
	case "preview":
		addPageFlags(fs, &limit, &after)
	case "review", "release":
		addAuditFlags(fs, &audit)
		fs.StringVar(&recoveryID, "recovery-id", "", "current externally supplied restore UUID")
		fs.Int64Var(&revision, "expected-revision", -1, "current recovery revision")
		if args[1] == "review" {
			fs.StringVar(&id, "id", "", "quarantined recipient UUID")
			fs.StringVar(&review.ExpectedStatus, "expected-status", "", "recipient status from preview")
			fs.StringVar(&review.Outcome, "outcome", "", "retry or submitted")
			fs.BoolVar(&review.ConfirmedNotAccepted, "confirmed-not-accepted", false, "positive evidence that relay did not accept; missing logs do not establish this")
			fs.BoolVar(&review.ConfirmedRelayAccepted, "confirmed-relay-accepted", false, "positive evidence of durable relay acceptance")
		} else {
			fs.BoolVar(&release.ConfirmedPolicy, "confirmed-policy", false, "suppression and all delivery policy restored and reviewed")
			fs.BoolVar(&release.ConfirmedAccess, "confirmed-access", false, "producer revocations, enrollments and credentials restored and reviewed")
			fs.BoolVar(&release.ConfirmedConsumers, "confirmed-consumers", false, "event consumers reviewed for rewind and replay")
			fs.BoolVar(&release.ConfirmedRelay, "confirmed-relay", false, "relay queue and evidence preserved and reviewed")
			fs.BoolVar(&release.ConfirmedQuarantine, "confirmed-quarantine", false, "unreviewed recipients remain blocked after release")
		}
	default:
		return command{}, fmt.Errorf("unknown recovery command; use mailctl help")
	}
	if err := fs.Parse(args[2:]); err != nil {
		return command{}, err
	}
	if fs.NArg() != 0 {
		return command{}, fmt.Errorf("positional arguments are not accepted")
	}
	switch args[1] {
	case "status":
		return command{http.MethodGet, internalmail.RecoveryPath, nil}, nil
	case "preview":
		if limit < 1 || limit > api.InternalMailMaxOperatorPageSize {
			return command{}, fmt.Errorf("invalid preview page size")
		}
		query := url.Values{"limit": {strconv.Itoa(limit)}}
		if after != "" {
			parsed, err := uuid.Parse(after)
			if err != nil || parsed == uuid.Nil || parsed.String() != after {
				return command{}, fmt.Errorf("invalid preview cursor")
			}
			query.Set("after", after)
		}
		return command{http.MethodGet, internalmail.RecoveryPath + "/preview?" + query.Encode(), nil}, nil
	case "review":
		review.OperatorAuditInput, review.RecoveryID, review.ExpectedRevision = audit, recoveryID, &revision
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id || review.Validate() != nil {
			return command{}, fmt.Errorf("review requires recipient, current recovery ID/revision/status, matching evidence confirmation and all audit flags")
		}
		return command{http.MethodPost, internalmail.RecoveryPath + "/recipients/" + id, review}, nil
	default:
		release.OperatorAuditInput, release.RecoveryID, release.ExpectedRevision = audit, recoveryID, &revision
		if release.Validate() != nil {
			return command{}, fmt.Errorf("release requires current recovery ID/revision, all five review confirmations and all audit flags")
		}
		return command{http.MethodPost, internalmail.RecoveryPath + "/release", release}, nil
	}
}

// adr: 643
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

type customerOperationRecoveryReceiptClient interface {
	BaseURL() string
	Whoami(context.Context) (api.AccountResponse, error)
	GetOperation(context.Context, string, string) (api.OperationResponse, error)
	RecoverOperationWithReceipt(context.Context, string, string, api.OperationRecoveryRequest) (api.OperationRecoveryDecision, error)
}
type customerOperationRecoveryReceipt struct {
	Version        int                          `json:"version"`
	API            string                       `json:"api"`
	AccountID      string                       `json:"account_id"`
	App            string                       `json:"app"`
	OperationID    string                       `json:"operation_id"`
	Request        api.OperationRecoveryRequest `json:"request"`
	CreatedAt      time.Time                    `json:"created_at"`
	ReplayNotAfter time.Time                    `json:"replay_not_after"`
}
type customerOperationRecoveryAcknowledgement struct {
	Version       int                           `json:"version"`
	RequestSHA256 string                        `json:"request_sha256"`
	Decision      api.OperationRecoveryDecision `json:"decision"`
}

func encodeCustomerOperationRecoveryReceipt(value any) ([]byte, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	if b.Len() > api.OperationRecoveryReceiptMaxBytes {
		return nil, fmt.Errorf("recovery receipt exceeds bounded size")
	}
	return b.Bytes(), nil
}

func loadCustomerOperationRecoveryReceiptFlags(c *customerOperationCommand, evidenceFile, resultFile string) error {
	if evidenceFile != "" {
		evidence, err := readCustomerOperationFile(evidenceFile, api.OperationRecoveryEvidenceMaxBytes)
		if err != nil {
			return err
		}
		if !utf8.Valid(evidence) || strings.TrimSpace(string(evidence)) == "" || bytes.ContainsRune(evidence, 0) {
			return fmt.Errorf("reconciliation evidence must be nonempty text")
		}
		c.recovery.Evidence = string(evidence)
	}
	if resultFile != "" {
		result, err := readCustomerOperationFile(resultFile, api.OperationSubmissionMaxBytes)
		if err != nil {
			return err
		}
		c.recovery.Result, err = operations.CanonicalJSON(result)
		if err != nil {
			return fmt.Errorf("invalid recovery result: %w", err)
		}
	}
	return nil
}

func validateCustomerOperationRecoveryRequest(req api.OperationRecoveryRequest) error {
	if !validCustomerOperationKey(req.RecoveryID) || req.ExpectedGeneration < 1 || req.ExpectedGeneration >= math.MaxInt32 || !utf8.ValidString(req.Evidence) || strings.TrimSpace(req.Evidence) == "" || len(req.Evidence) > api.OperationRecoveryEvidenceMaxBytes || strings.ContainsRune(req.Evidence, 0) {
		return fmt.Errorf("new recovery receipt requires a valid decision ID, generation, resolution and evidence")
	}
	if rev := req.ExpectedInspectionRevision; rev != "" {
		if len(rev) != 71 || !strings.HasPrefix(rev, "sha256:") || strings.Trim(rev[7:], "0123456789abcdef") != "" {
			return fmt.Errorf("invalid inspection revision")
		}
	}
	switch req.Resolution {
	case "succeeded":
		if len(req.Result) == 0 || len(req.Result) > api.OperationSubmissionMaxBytes {
			return fmt.Errorf("succeeded requires a bounded JSON result")
		}
		if _, err := operations.CanonicalJSON(req.Result); err != nil {
			return err
		}
	case "failed", "cancelled", "safe_to_retry":
		if len(req.Result) > 0 {
			return fmt.Errorf("only succeeded accepts a result")
		}
	default:
		return fmt.Errorf("invalid recovery resolution")
	}
	return nil
}

func validateCustomerOperationRecoveryReceipt(r customerOperationRecoveryReceipt, c customerOperationCommand, origin, account string) error {
	if r.Version != 1 || r.API != origin || !sameCustomerOperationUUID(r.AccountID, account) || r.App != c.app || !sameCustomerOperationUUID(r.OperationID, c.id) || r.CreatedAt.IsZero() || !r.ReplayNotAfter.After(r.CreatedAt) {
		return fmt.Errorf("recovery receipt is invalid or bound to another API, account or operation")
	}
	if err := validateCustomerOperationRecoveryRequest(r.Request); err != nil {
		return err
	}
	req := c.recovery
	selectors := map[string]bool{
		"recovery-id":         req.RecoveryID == r.Request.RecoveryID,
		"expected-generation": req.ExpectedGeneration == r.Request.ExpectedGeneration,
		"resolution":          req.Resolution == r.Request.Resolution,
		"inspection-revision": req.ExpectedInspectionRevision == r.Request.ExpectedInspectionRevision,
		"evidence-file":       req.Evidence == r.Request.Evidence,
	}
	for flag, matches := range selectors {
		if c.recoveryFlags[flag] && !matches {
			return fmt.Errorf("--%s conflicts with the immutable recovery receipt", flag)
		}
	}
	if c.recoveryFlags["result-file"] {
		a, ea := operations.InputFingerprint(req.Result)
		b, eb := operations.InputFingerprint(r.Request.Result)
		if ea != nil || eb != nil || a != b {
			return fmt.Errorf("--result-file conflicts with the immutable recovery receipt")
		}
	}
	return nil
}

func prepareCustomerOperationRecoveryReceipt(ctx context.Context, client customerOperationRecoveryReceiptClient, c customerOperationCommand, origin, account string, now time.Time) (customerOperationRecoveryReceipt, []byte, error) {
	var receipt customerOperationRecoveryReceipt
	raw, err := readCustomerOperationPrivateReceiptBound(c.receipt, api.OperationRecoveryReceiptMaxBytes)
	if errors.Is(err, os.ErrNotExist) {
		if err = validateCustomerOperationRecoveryRequest(c.recovery); err != nil {
			return receipt, nil, err
		}
		if _, ackErr := os.Lstat(c.receipt + ".decided.json"); ackErr == nil || !errors.Is(ackErr, os.ErrNotExist) {
			raw, err = readCustomerOperationPrivateReceiptBound(c.receipt, api.OperationRecoveryReceiptMaxBytes)
			if err != nil {
				return receipt, nil, fmt.Errorf("acknowledgement exists without a readable recovery request")
			}
		} else {
			op, e := client.GetOperation(ctx, c.app, c.id)
			if e != nil {
				return receipt, nil, e
			}
			if !sameCustomerOperationUUID(op.ID, c.id) || op.State != api.OperationRequiresReconciliation || op.Generation != c.recovery.ExpectedGeneration || !op.ExpiresAt.After(now) {
				return receipt, nil, fmt.Errorf("refresh operation: selected reconciliation generation is stale or expired")
			}
			receipt = customerOperationRecoveryReceipt{Version: 1, API: origin, AccountID: account, App: c.app, OperationID: op.ID, Request: c.recovery, CreatedAt: now, ReplayNotAfter: op.ExpiresAt}
			raw, err = encodeCustomerOperationRecoveryReceipt(receipt)
			if err == nil {
				err = publishCustomerOperationReceipt(c.receipt, raw)
			}
			if err == nil || errors.Is(err, os.ErrExist) {
				raw, err = readCustomerOperationPrivateReceiptBound(c.receipt, api.OperationRecoveryReceiptMaxBytes)
			}
		}
	}
	if err != nil {
		return receipt, nil, err
	}
	receipt = customerOperationRecoveryReceipt{}
	if err = decodeCustomerOperationReceipt(raw, &receipt); err == nil {
		err = validateCustomerOperationRecoveryReceipt(receipt, c, origin, account)
	}
	return receipt, raw, err
}

func validateCustomerOperationRecoveryDecision(d api.OperationRecoveryDecision, r customerOperationRecoveryReceipt) error {
	raw, err := json.Marshal(r.Request)
	if err != nil {
		return err
	}
	fingerprint, err := operations.InputFingerprint(raw)
	if err != nil {
		return err
	}
	generation, state := r.Request.ExpectedGeneration, api.OperationState(r.Request.Resolution)
	if r.Request.Resolution == "safe_to_retry" {
		generation++
		state = api.OperationAccepted
	}
	executionIDs := []string{d.InvocationID, d.WorkflowRunID, d.JobRunID}
	validExecution, count := true, 0
	for _, id := range executionIDs {
		if id != "" {
			count++
			validExecution = validExecution && validCustomerOperationUUID(id)
		}
	}
	validExecution = validExecution && count == 1
	if !sameCustomerOperationUUID(d.OperationID, r.OperationID) || d.RecoveryID != r.Request.RecoveryID || d.RequestFingerprint != fingerprint || d.ExpectedGeneration != r.Request.ExpectedGeneration || d.Generation != generation || d.ExpectedInspectionRevision != r.Request.ExpectedInspectionRevision || d.Resolution != r.Request.Resolution || d.State != state || d.RecordedAt.IsZero() || !d.ExpiresAt.Equal(r.ReplayNotAfter) || !d.ExpiresAt.After(d.RecordedAt) || !validExecution {
		return fmt.Errorf("recovery acknowledgement does not match the recorded decision")
	}
	return nil
}

func readCustomerOperationRecoveryAcknowledgement(path, digest string, r customerOperationRecoveryReceipt) (api.OperationRecoveryDecision, error) {
	var ack customerOperationRecoveryAcknowledgement
	raw, err := readCustomerOperationPrivateReceiptBound(path, api.OperationRecoveryReceiptMaxBytes)
	if err != nil {
		return ack.Decision, err
	}
	if err = decodeCustomerOperationReceipt(raw, &ack); err != nil {
		return ack.Decision, err
	}
	if ack.Version != 1 || ack.RequestSHA256 != digest {
		return ack.Decision, fmt.Errorf("acknowledgement belongs to another recovery receipt")
	}
	return ack.Decision, validateCustomerOperationRecoveryDecision(ack.Decision, r)
}

func recoverCustomerOperationWithReceipt(ctx context.Context, client customerOperationRecoveryReceiptClient, c customerOperationCommand, now time.Time) (api.OperationRecoveryDecision, error) {
	empty := api.OperationRecoveryDecision{}
	origin, err := customerOperationAPIOrigin(client.BaseURL())
	if err != nil {
		return empty, err
	}
	identity, err := client.Whoami(ctx)
	if err != nil {
		return empty, fmt.Errorf("verify recovery account: %w", err)
	}
	if !validCustomerOperationUUID(identity.ID) {
		return empty, fmt.Errorf("invalid recovery account identity")
	}
	receipt, raw, err := prepareCustomerOperationRecoveryReceipt(ctx, client, c, origin, identity.ID, now)
	if err != nil {
		return empty, err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	ackPath := c.receipt + ".decided.json"
	decision, err := readCustomerOperationRecoveryAcknowledgement(ackPath, digest, receipt)
	if err == nil {
		return decision, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	if now.Before(receipt.CreatedAt) || !now.Before(receipt.ReplayNotAfter) {
		return empty, fmt.Errorf("unconfirmed recovery receipt expired; inspect retained work before choosing another decision")
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	decision, err = client.RecoverOperationWithReceipt(ctx, c.app, receipt.OperationID, receipt.Request)
	if err != nil {
		return empty, fmt.Errorf("recover operation; resume with the same --receipt-file: %w", err)
	}
	if err = validateCustomerOperationRecoveryDecision(decision, receipt); err != nil {
		return empty, err
	}
	body, err := encodeCustomerOperationRecoveryReceipt(customerOperationRecoveryAcknowledgement{Version: 1, RequestSHA256: digest, Decision: decision})
	if err == nil {
		err = publishCustomerOperationReceipt(ackPath, body)
	}
	if errors.Is(err, os.ErrExist) {
		saved, e := readCustomerOperationRecoveryAcknowledgement(ackPath, digest, receipt)
		if e == nil && saved != decision {
			e = fmt.Errorf("saved recovery decision conflicts with server acknowledgement")
		}
		return saved, e
	}
	if err != nil {
		return empty, fmt.Errorf("recovery was recorded but acknowledgement could not be saved; resume the receipt: %w", err)
	}
	return decision, nil
}

func runCustomerOperationRecoveryReceipt(ctx context.Context, client customerOperationsClient, c customerOperationCommand, out io.Writer, asJSON bool, now time.Time) (int, error) {
	receiptClient, ok := client.(customerOperationRecoveryReceiptClient)
	if !ok {
		return 0, fmt.Errorf("recovery receipts require account credentials and a supported client")
	}
	decision, err := recoverCustomerOperationWithReceipt(ctx, receiptClient, c, now)
	if err != nil {
		return 0, err
	}
	if asJSON {
		return 0, json.NewEncoder(out).Encode(decision)
	}
	_, err = fmt.Fprintf(out, "Recovery decision recorded: %s operation=%s resolution=%s generation=%d->%d state_at_decision=%s\nRead current work with customer-operations get.\n", decision.RecoveryID, decision.OperationID, decision.Resolution, decision.ExpectedGeneration, decision.Generation, decision.State)
	return 0, err
}

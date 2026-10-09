package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

type customerOperationSubmissionClient interface {
	BaseURL() string
	GetPlatformTenantSelfOperationIdentity(context.Context) (api.OperationTenantIdentity, error)
	StartPlatformTenantSelfOperation(context.Context, api.OperationStartRequest, string) (api.OperationAcceptedResponse, error)
}

type customerOperationSubmissionReceipt struct {
	Version        int                         `json:"version"`
	API            string                      `json:"api"`
	Identity       api.OperationTenantIdentity `json:"identity"`
	DefinitionID   string                      `json:"definition_id"`
	IdempotencyKey string                      `json:"idempotency_key"`
	Input          json.RawMessage             `json:"input"`
	InputSHA256    string                      `json:"input_sha256"`
	CreatedAt      time.Time                   `json:"created_at"`
	ReplayNotAfter time.Time                   `json:"replay_not_after"`
}

type customerOperationSubmissionAcknowledgement struct {
	Version       int                           `json:"version"`
	RequestSHA256 string                        `json:"request_sha256"`
	Accepted      api.OperationAcceptedResponse `json:"accepted"`
}

func startCustomerOperation(ctx context.Context, client customerOperationSubmissionClient, c customerOperationDeveloperCommand, now time.Time) (api.OperationAcceptedResponse, error) {
	var empty api.OperationAcceptedResponse
	origin, err := customerOperationAPIOrigin(client.BaseURL())
	if err != nil {
		return empty, err
	}
	var input json.RawMessage
	if c.input != "" {
		body, err := readCustomerOperationFile(c.input, api.OperationSubmissionMaxBytes)
		if err != nil {
			return empty, err
		}
		input, err = operations.CanonicalJSON(body)
		if err != nil {
			return empty, fmt.Errorf("canonicalize operation input: %w", err)
		}
		if len(input) > api.OperationSubmissionMaxBytes || !validCustomerOperationUUID(c.definition) || !validCustomerOperationKey(c.key) {
			return empty, fmt.Errorf("invalid definition, input size or idempotency key")
		}
	}
	identity, err := client.GetPlatformTenantSelfOperationIdentity(ctx)
	if err != nil {
		return empty, fmt.Errorf("verify submission tenant: %w", err)
	}
	if !validCustomerOperationUUID(identity.AccountID) || !validCustomerOperationUUID(identity.PlatformTenantID) {
		return empty, fmt.Errorf("invalid authenticated tenant identity")
	}
	receipt, raw, err := prepareCustomerOperationSubmission(c, origin, identity, input, now)
	if err != nil {
		return empty, err
	}
	requestDigest := fmt.Sprintf("%x", sha256.Sum256(raw))
	acknowledgementPath := c.receipt + ".accepted.json"
	ack, err := readCustomerOperationAcknowledgement(acknowledgementPath, requestDigest)
	if err == nil {
		return ack.Accepted, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	if now.Before(receipt.CreatedAt) || !now.Before(receipt.ReplayNotAfter) {
		return empty, fmt.Errorf("unconfirmed receipt is outside its safe replay window; inspect retained work before another submission")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	accepted, err := client.StartPlatformTenantSelfOperation(ctx, api.OperationStartRequest{DefinitionID: receipt.DefinitionID, Input: receipt.Input}, receipt.IdempotencyKey)
	if err != nil {
		return empty, fmt.Errorf("submit operation; resume using the same --receipt-file: %w", err)
	}
	ack = customerOperationSubmissionAcknowledgement{Version: 1, RequestSHA256: requestDigest, Accepted: accepted}
	if err := validateCustomerOperationAcknowledgement(ack, requestDigest); err != nil {
		return empty, err
	}
	body, err := encodeCustomerOperationReceipt(ack)
	if err == nil {
		err = publishCustomerOperationReceipt(acknowledgementPath, body)
	}
	if errors.Is(err, os.ErrExist) {
		var existing customerOperationSubmissionAcknowledgement
		existing, err = readCustomerOperationAcknowledgement(acknowledgementPath, requestDigest)
		if err == nil && existing.Accepted != accepted {
			err = fmt.Errorf("accepted receipt conflicts with the server response")
		}
	}
	if err != nil {
		return accepted, fmt.Errorf("operation %s was accepted, but saving its acknowledgement failed; preserve the request receipt: %w", accepted.ID, err)
	}
	return accepted, nil
}

func prepareCustomerOperationSubmission(c customerOperationDeveloperCommand, origin string, identity api.OperationTenantIdentity, input json.RawMessage, now time.Time) (customerOperationSubmissionReceipt, []byte, error) {
	var receipt customerOperationSubmissionReceipt
	raw, err := readCustomerOperationPrivateReceipt(c.receipt)
	if errors.Is(err, os.ErrNotExist) {
		if c.definition == "" || c.key == "" || len(input) == 0 {
			return receipt, nil, fmt.Errorf("new receipt requires --definition, --input-file and --idempotency-key")
		}
		if _, ackErr := os.Lstat(c.receipt + ".accepted.json"); ackErr == nil || !errors.Is(ackErr, os.ErrNotExist) {
			// Another process may have published both files since our first read.
			raw, err = readCustomerOperationPrivateReceipt(c.receipt)
			if err != nil {
				return receipt, nil, fmt.Errorf("acknowledgement exists without a readable request receipt")
			}
		} else {
			var fingerprint string
			fingerprint, err = operations.InputFingerprint(input)
			if err != nil {
				return receipt, nil, err
			}
			receipt = customerOperationSubmissionReceipt{Version: 1, API: origin, Identity: identity, DefinitionID: c.definition, IdempotencyKey: c.key, Input: input, InputSHA256: fingerprint, CreatedAt: now, ReplayNotAfter: now.Add(customerOperationSafeReplayWindow())}
			raw, err = encodeCustomerOperationReceipt(receipt)
			if err != nil {
				return receipt, nil, err
			}
			if err := publishCustomerOperationReceipt(c.receipt, raw); err != nil && !errors.Is(err, os.ErrExist) {
				return receipt, nil, fmt.Errorf("save request receipt before submission: %w", err)
			}
			// Also read the winner after concurrent publication; all requests must use
			// the same durable creation time and exact original definition/input.
			raw, err = readCustomerOperationPrivateReceipt(c.receipt)
		}
	}
	if err != nil {
		return receipt, nil, fmt.Errorf("read request receipt: %w", err)
	}
	if err := decodeCustomerOperationReceipt(raw, &receipt); err != nil {
		return receipt, nil, err
	}
	if err := validateCustomerOperationSubmissionReceipt(receipt); err != nil {
		return receipt, nil, err
	}
	if receipt.API != origin || receipt.Identity != identity {
		return receipt, nil, fmt.Errorf("receipt belongs to a different API or authenticated tenant")
	}
	if c.definition != "" {
		fingerprint, err := operations.InputFingerprint(input)
		if err != nil {
			return receipt, nil, err
		}
		if c.definition != receipt.DefinitionID || c.key != receipt.IdempotencyKey || fingerprint != receipt.InputSHA256 {
			return receipt, nil, fmt.Errorf("definition, key or input conflicts with the immutable request receipt")
		}
	}
	return receipt, raw, nil
}

func validCustomerOperationUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}
func validCustomerOperationKey(key string) bool {
	return key != "" && len(key) <= api.OperationIdempotencyKeyMaxBytes && !strings.ContainsAny(key, "\x00\r\n")
}

func customerOperationAPIOrigin(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(value) > api.OperationPathMaxBytes {
		return "", fmt.Errorf("API URL cannot contain credentials, query or fragment and must be bounded HTTP(S)")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// Use the minimum enabled-plan retention, not the current plan's larger
// allowance. Downgrades cannot make an unconfirmed receipt silently start new work.
func customerOperationSafeReplayWindow() time.Duration {
	minimum := 0
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		limits := api.MustLimitsFor(plan).Operations
		if limits.Allowed && (minimum == 0 || limits.IdempotencyRetentionSeconds < minimum) {
			minimum = limits.IdempotencyRetentionSeconds
		}
	}
	return time.Duration(minimum) * time.Second
}

func validateCustomerOperationSubmissionReceipt(r customerOperationSubmissionReceipt) error {
	origin, err := customerOperationAPIOrigin(r.API)
	if err != nil || origin != r.API || r.Version != 1 || !validCustomerOperationUUID(r.Identity.AccountID) || !validCustomerOperationUUID(r.Identity.PlatformTenantID) || !validCustomerOperationUUID(r.DefinitionID) || !validCustomerOperationKey(r.IdempotencyKey) || len(r.Input) > api.OperationSubmissionMaxBytes || r.CreatedAt.IsZero() || !r.ReplayNotAfter.After(r.CreatedAt) || r.ReplayNotAfter.After(r.CreatedAt.Add(customerOperationSafeReplayWindow())) {
		return fmt.Errorf("invalid submission receipt")
	}
	fingerprint, err := operations.InputFingerprint(r.Input)
	if err != nil || fingerprint != r.InputSHA256 {
		return fmt.Errorf("receipt input fingerprint is invalid")
	}
	return nil
}

func validateCustomerOperationAcknowledgement(a customerOperationSubmissionAcknowledgement, digest string) error {
	if a.Version != 1 || a.RequestSHA256 != digest || !validCustomerOperationUUID(a.Accepted.ID) || a.Accepted.StatusURL != "/v1/platform-tenant-self/customer-operations/"+a.Accepted.ID || a.Accepted.EventsURL != a.Accepted.StatusURL+"/events" {
		return fmt.Errorf("accepted receipt does not match its request or operation identity")
	}
	return nil
}

func readCustomerOperationAcknowledgement(path, digest string) (customerOperationSubmissionAcknowledgement, error) {
	var ack customerOperationSubmissionAcknowledgement
	body, err := readCustomerOperationPrivateReceipt(path)
	if err != nil {
		return ack, err
	}
	if err := decodeCustomerOperationReceipt(body, &ack); err != nil {
		return ack, err
	}
	return ack, validateCustomerOperationAcknowledgement(ack, digest)
}

func encodeCustomerOperationReceipt(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode submission receipt: %w", err)
	}
	if buffer.Len() > api.OperationSubmissionReceiptMaxBytes {
		return nil, fmt.Errorf("submission receipt exceeds bounded size")
	}
	return buffer.Bytes(), nil
}

func decodeCustomerOperationReceipt(raw []byte, value any) error {
	// Validate metadata independently: a payload may already use the protocol's
	// full nesting allowance. Adding the receipt envelope must not reduce it.
	reader := json.NewDecoder(bytes.NewReader(raw))
	start, err := reader.Token()
	if err != nil || start != json.Delim('{') {
		return fmt.Errorf("receipt must be a JSON object")
	}
	metadata := map[string]json.RawMessage{}
	for reader.More() {
		token, err := reader.Token()
		if err != nil {
			return fmt.Errorf("read receipt field: %w", err)
		}
		name, ok := token.(string)
		if !ok {
			return fmt.Errorf("invalid receipt field")
		}
		if _, exists := metadata[name]; exists {
			return fmt.Errorf("duplicate receipt field")
		}
		var field json.RawMessage
		if err := reader.Decode(&field); err != nil {
			return fmt.Errorf("read receipt value: %w", err)
		}
		switch name {
		case "request":
			// Validate a control request at the same depth as its HTTP body;
			// wrapping a typed recovery result in a local receipt adds no limit.
			if _, err := operations.CanonicalJSON(field); err != nil {
				return fmt.Errorf("invalid receipt request: %w", err)
			}
			field = json.RawMessage(`null`)
		case "input":
			field = json.RawMessage(`null`)
		}
		metadata[name] = field
	}
	if _, err := reader.Token(); err != nil {
		return fmt.Errorf("close receipt: %w", err)
	}
	if _, err := reader.Token(); err != io.EOF {
		return fmt.Errorf("receipt must contain exactly one JSON object")
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode receipt metadata: %w", err)
	}
	if _, err := operations.CanonicalJSON(encoded); err != nil {
		return fmt.Errorf("invalid receipt metadata: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode receipt: %w", err)
	}
	return nil
}

func readCustomerOperationPrivateReceipt(path string) ([]byte, error) {
	return readCustomerOperationPrivateReceiptBound(path, api.OperationSubmissionReceiptMaxBytes)
}

func readCustomerOperationPrivateReceiptBound(path string, maxBytes int64) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("receipt must be private to its owner (0600)")
	}
	return readCustomerOperationSourceFile(root, filepath.Base(path), maxBytes)
}

// Request and acknowledgement files are immutable. Exclusive publication plus
// file/directory fsync completes before any POST; interruption cannot erase the key.
func publishCustomerOperationReceipt(path string, body []byte) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	name := ".gregale-submission-" + uuid.NewString()
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = root.Remove(name) }()
	if _, err := io.Copy(file, bytes.NewReader(body)); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := root.Link(name, filepath.Base(path)); err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}

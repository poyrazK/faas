package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type customerOperationDeliveryReceipt struct {
	Version        int                               `json:"version"`
	API            string                            `json:"api"`
	AccountID      string                            `json:"account_id"`
	App            string                            `json:"app"`
	OperationID    string                            `json:"operation_id"`
	Request        api.OperationDeliveryRetryRequest `json:"request"`
	CreatedAt      time.Time                         `json:"created_at"`
	ReplayNotAfter time.Time                         `json:"replay_not_after"`
}
type customerOperationDeliveryAcknowledgement struct {
	Version       int                                `json:"version"`
	RequestSHA256 string                             `json:"request_sha256"`
	Decision      api.OperationDeliveryRetryResponse `json:"decision"`
}

func encodeCustomerOperationDeliveryReceipt(value any) ([]byte, error) {
	body, err := encodeCustomerOperationReceipt(value)
	if err == nil && len(body) > api.OperationDeliveryReceiptMaxBytes {
		return nil, fmt.Errorf("delivery receipt exceeds bounded size")
	}
	return body, err
}

func sameCustomerOperationUUID(a, b string) bool {
	x, e := uuid.Parse(a)
	y, f := uuid.Parse(b)
	return e == nil && f == nil && x != uuid.Nil && x == y
}

func validateCustomerOperationDeliveryReceipt(r customerOperationDeliveryReceipt, c customerOperationDeliveryCommand, origin, account string) error {
	req := r.Request
	if r.Version != 1 || r.API != origin || !sameCustomerOperationUUID(r.AccountID, account) || r.App != c.app || !sameCustomerOperationUUID(r.OperationID, c.id) || !validCustomerOperationKey(req.RetryID) || !validCustomerOperationUUID(req.DeliveryID) || req.ExpectedReplayGeneration == nil || *req.ExpectedReplayGeneration < 0 || *req.ExpectedReplayGeneration >= math.MaxInt32 || r.CreatedAt.IsZero() || !r.ReplayNotAfter.After(r.CreatedAt) {
		return fmt.Errorf("retry receipt is invalid or bound to another API, account or operation")
	}
	if c.request.ExpectedReplayGeneration != nil && (c.request.RetryID != req.RetryID || !sameCustomerOperationUUID(c.request.DeliveryID, req.DeliveryID) || *c.request.ExpectedReplayGeneration != *req.ExpectedReplayGeneration) {
		return fmt.Errorf("retry flags conflict with the immutable receipt")
	}
	return nil
}

func validateCustomerOperationDeliveryDecision(r api.OperationDeliveryRetryResponse, receipt customerOperationDeliveryReceipt) error {
	req := receipt.Request
	if !sameCustomerOperationUUID(r.OperationID, receipt.OperationID) || !sameCustomerOperationUUID(r.DeliveryID, req.DeliveryID) || r.RetryID != req.RetryID || r.ExpectedReplayGeneration != *req.ExpectedReplayGeneration || r.ReplayGeneration != r.ExpectedReplayGeneration+1 || r.State != "queued" || r.QueuedAt.IsZero() || !r.ExpiresAt.Equal(receipt.ReplayNotAfter) || !r.ExpiresAt.After(r.QueuedAt) {
		return fmt.Errorf("retry acknowledgement does not match its recorded request")
	}
	return nil
}

func prepareCustomerOperationDeliveryReceipt(ctx context.Context, client customerOperationDeliveryClient, c customerOperationDeliveryCommand, origin, account string, now time.Time) (customerOperationDeliveryReceipt, []byte, error) {
	var receipt customerOperationDeliveryReceipt
	raw, err := readCustomerOperationPrivateReceiptBound(c.receipt, api.OperationDeliveryReceiptMaxBytes)
	if errors.Is(err, os.ErrNotExist) {
		if c.request.ExpectedReplayGeneration == nil {
			return receipt, nil, fmt.Errorf("receipt is missing; provide all retry selectors for a new request")
		}
		report, e := client.GetOperationDelivery(ctx, c.app, c.id)
		if e != nil {
			return receipt, nil, e
		}
		if !sameCustomerOperationUUID(report.OperationID, c.id) || !sameCustomerOperationUUID(report.DeliveryID, c.request.DeliveryID) || report.State != "dead" || report.ReplayGeneration == nil || *report.ReplayGeneration != *c.request.ExpectedReplayGeneration || !report.OperationExpiresAt.After(now) {
			return receipt, nil, fmt.Errorf("refresh delivery: selected dead generation is stale or expired")
		}
		receipt = customerOperationDeliveryReceipt{Version: 1, API: origin, AccountID: account, App: c.app, OperationID: report.OperationID, Request: c.request, CreatedAt: now, ReplayNotAfter: report.OperationExpiresAt}
		raw, err = encodeCustomerOperationDeliveryReceipt(receipt)
		if err == nil {
			err = publishCustomerOperationReceipt(c.receipt, raw)
		}
		if err == nil || errors.Is(err, os.ErrExist) {
			raw, err = readCustomerOperationPrivateReceiptBound(c.receipt, api.OperationDeliveryReceiptMaxBytes)
		}
	}
	if err != nil {
		return receipt, nil, err
	}
	// Decode into fresh storage: the command request contains a caller-owned
	// generation pointer, which concurrent retries must never mutate.
	receipt = customerOperationDeliveryReceipt{}
	if err = decodeCustomerOperationReceipt(raw, &receipt); err == nil {
		err = validateCustomerOperationDeliveryReceipt(receipt, c, origin, account)
	}
	return receipt, raw, err
}

func readCustomerOperationDeliveryAcknowledgement(path, digest string, r customerOperationDeliveryReceipt) (api.OperationDeliveryRetryResponse, error) {
	var ack customerOperationDeliveryAcknowledgement
	raw, err := readCustomerOperationPrivateReceiptBound(path, api.OperationDeliveryReceiptMaxBytes)
	if err != nil {
		return ack.Decision, err
	}
	if err = decodeCustomerOperationReceipt(raw, &ack); err != nil {
		return ack.Decision, err
	}
	if ack.Version != 1 || ack.RequestSHA256 != digest {
		return ack.Decision, fmt.Errorf("acknowledgement belongs to another retry receipt")
	}
	return ack.Decision, validateCustomerOperationDeliveryDecision(ack.Decision, r)
}

func retryCustomerOperationDelivery(ctx context.Context, client customerOperationDeliveryClient, c customerOperationDeliveryCommand, now time.Time) (api.OperationDeliveryRetryResponse, error) {
	empty := api.OperationDeliveryRetryResponse{}
	origin, err := customerOperationAPIOrigin(client.BaseURL())
	if err != nil {
		return empty, err
	}
	identity, err := client.Whoami(ctx)
	if err != nil {
		return empty, fmt.Errorf("verify retry account: %w", err)
	}
	if !validCustomerOperationUUID(identity.ID) {
		return empty, fmt.Errorf("invalid retry account identity")
	}
	receipt, raw, err := prepareCustomerOperationDeliveryReceipt(ctx, client, c, origin, identity.ID, now)
	if err != nil {
		return empty, err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	ackPath := c.receipt + ".queued.json"
	decision, err := readCustomerOperationDeliveryAcknowledgement(ackPath, digest, receipt)
	if err == nil {
		return decision, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	if now.Before(receipt.CreatedAt) || !now.Before(receipt.ReplayNotAfter) {
		return empty, fmt.Errorf("unconfirmed retry receipt expired; inspect delivery before choosing another decision")
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	decision, err = client.RetryOperationDeliveryWithReceipt(ctx, c.app, receipt.OperationID, receipt.Request)
	if err != nil {
		return empty, fmt.Errorf("retry delivery; resume with the same --receipt-file: %w", err)
	}
	if err = validateCustomerOperationDeliveryDecision(decision, receipt); err != nil {
		return empty, err
	}
	ack := customerOperationDeliveryAcknowledgement{Version: 1, RequestSHA256: digest, Decision: decision}
	body, err := encodeCustomerOperationDeliveryReceipt(ack)
	if err == nil {
		err = publishCustomerOperationReceipt(ackPath, body)
	}
	if errors.Is(err, os.ErrExist) {
		return readCustomerOperationDeliveryAcknowledgement(ackPath, digest, receipt)
	}
	if err != nil {
		return empty, fmt.Errorf("retry was recorded but acknowledgement could not be saved; resume the receipt: %w", err)
	}
	return decision, nil
}

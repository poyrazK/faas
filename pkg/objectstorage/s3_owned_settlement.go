package objectstorage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
	"github.com/onebox-faas/faas/pkg/state"
)

func NewWriteReceiptID() string { return objectrecovery.NewID() }

// QueueOwnedNodeSettlement opts new admissions into durable delivery. Saved
// jobs may still drain after this switch is disabled, while receipts stay on.
func QueueOwnedNodeSettlement(provider Provider) bool {
	p, owned := provider.(*S3)
	return owned && p.ownedNodeReceipts && p.ownedNodeSettlements
}

// AcknowledgeSettledUpload is an explicit retry for a historical terminal
// record. Automatic delivery uses the state owner's durable outbox.
func AcknowledgeSettledUpload(ctx context.Context, provider Provider, bucket string, c state.ObjectUploadCompletion) error {
	if !QueueOwnedNodeSettlement(provider) {
		return nil
	}
	if _, bounded := objectrecovery.IssuedAt(c.ID); !bounded {
		return nil
	}
	a, ready, err := state.UploadNodeSettlement(bucket, c)
	if err != nil {
		return ErrInvalid
	}
	if !ready {
		return nil
	}
	return provider.(*S3).AcknowledgeOwnedSettlement(ctx, a)
}

func AcknowledgeSettledMultipart(ctx context.Context, provider Provider, bucket string, u state.ObjectMultipartUpload) error {
	if !QueueOwnedNodeSettlement(provider) {
		return nil
	}
	if _, bounded := objectrecovery.IssuedAt(u.ID); !bounded {
		return nil
	}
	if u.ProviderUploadID != ownedMultipartID(u.ID) {
		return ErrConfiguration
	}
	a, ready, err := state.MultipartNodeSettlement(bucket, u)
	if err != nil {
		return ErrInvalid
	}
	if !ready {
		return nil
	}
	return provider.(*S3).AcknowledgeOwnedSettlement(ctx, a)
}

// AcknowledgeOwnedSettlement is also the explicit retry boundary for an already
// settled gateway record. It performs one signed attempt, with no redirect or
// automatic replay, and validates the exact acknowledgement.
func (p *S3) AcknowledgeOwnedSettlement(parent context.Context, a objectrecovery.Settlement) error {
	options := p.client.Options()
	if !p.ownedNodeReceipts || options.BaseEndpoint == nil {
		return ErrConfiguration
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), api.ObjectNodeRecoveryAckTimeout)
	defer cancel()
	u, err := url.Parse(*options.BaseEndpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" {
		return ErrConfiguration
	}
	u.Path, u.RawPath, u.RawQuery = strings.TrimRight(u.Path, "/")+"/", "", url.Values{objectrecovery.Query: {"settle"}}.Encode()
	body, err := json.Marshal(a)
	if err != nil || len(body) > api.ObjectNodeRecoveryRequestMaxBytes {
		return ErrInvalid
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return ErrInvalid
	}
	credential, err := options.Credentials.Retrieve(ctx)
	if err != nil {
		return ErrConfiguration
	}
	digest := sha256.Sum256(body)
	payload := hex.EncodeToString(digest[:])
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Amz-Content-Sha256", payload)
	if err = v4.NewSigner().SignHTTP(ctx, credential, r, payload, "s3", p.region, time.Now().UTC()); err != nil {
		return ErrConfiguration
	}
	response, err := options.HTTPClient.Do(r)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return ErrConfiguration
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" {
		return ErrUnavailable
	}
	reply, err := io.ReadAll(io.LimitReader(response.Body, api.ObjectNodeRecoveryResponseMaxBytes+1))
	if err != nil || len(reply) > api.ObjectNodeRecoveryResponseMaxBytes {
		return ErrUnavailable
	}
	var ack objectrecovery.Acknowledgement
	decoder := json.NewDecoder(bytes.NewReader(reply))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&ack) != nil || decoder.Decode(new(any)) != io.EOF || ack.Version != 1 || ack.ID != a.ID || ack.Status != a.Status || ack.SettledAt.IsZero() || ack.SettledAt.After(time.Now().Add(api.ObjectNodeRecoveryClockSkew)) {
		return ErrUnavailable
	}
	return nil
}

package objectnode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/onebox-faas/faas/pkg/api"
)

// HTTPReplicationSource uses only the private operator SigV4 extension. It never
// follows redirects or falls back to an ordinary S3 object/listing operation.
type HTTPReplicationSource struct {
	endpoint   *url.URL
	region     string
	credential aws.Credentials
	client     *http.Client
	batch      atomic.Bool
}

func NewHTTPReplicationSource(endpoint, region, accessKey, secret string) (*HTTPReplicationSource, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || u.RawPath != "" || region == "" || accessKey == "" || secret == "" {
		return nil, ErrInvalid
	}
	return &HTTPReplicationSource{endpoint: u, region: region, credential: aws.Credentials{AccessKeyID: accessKey, SecretAccessKey: secret}, client: &http.Client{Timeout: api.ObjectNodeReplicationRequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *HTTPReplicationSource) request(ctx context.Context, method string, query url.Values, contentType string) (*http.Response, error) {
	return c.requestBody(ctx, method, query, contentType, nil)
}

func (c *HTTPReplicationSource) requestBody(ctx context.Context, method string, query url.Values, contentType string, body []byte) (*http.Response, error) {
	u := *c.endpoint
	u.Path, u.RawQuery = "/", query.Encode()
	r, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	empty := nameHash(string(body))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("X-Amz-Content-Sha256", empty)
	if err = v4.NewSigner().SignHTTP(ctx, c.credential, r, empty, "s3", c.region, time.Now().UTC()); err != nil {
		return nil, err
	}
	response, err := c.client.Do(r)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != contentType {
		_ = response.Body.Close()
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusBadRequest {
			return nil, ErrInvalid
		}
		if response.StatusCode == http.StatusConflict {
			return nil, ErrReplicationFork
		}
		if response.StatusCode == http.StatusGone {
			return nil, ErrReplicaReseed
		}
		if response.StatusCode == http.StatusNotFound {
			return nil, ErrCorrupt
		}
		return nil, replicationError(response.StatusCode)
	}
	return response, nil
}

func (c *HTTPReplicationSource) ReportFollower(ctx context.Context, report FollowerReport) (RegisteredFollower, error) {
	var result RegisteredFollower
	if !validFollowerReport(report) {
		return result, ErrInvalid
	}
	body, err := json.Marshal(report)
	if err != nil || len(body) > api.ObjectNodeReplicaAckMaxBytes {
		return result, ErrInvalid
	}
	err = c.jsonBody(ctx, http.MethodPost, url.Values{ReplicationQuery: {"cursor"}}, body, &result)
	if err == nil && (result.ReplicaID != report.ReplicaID || result.RegistrationID != report.RegistrationID || result.Sequence != report.Sequence || result.Hash != report.Hash || result.UpdatedAt.IsZero()) {
		err = ErrReplicationFork
	}
	return result, err
}

func (c *HTTPReplicationSource) json(ctx context.Context, query url.Values, value any) error {
	return c.jsonRequest(ctx, http.MethodGet, query, value)
}

func (c *HTTPReplicationSource) jsonRequest(ctx context.Context, method string, query url.Values, value any) error {
	return c.jsonBody(ctx, method, query, nil, value)
}

func (c *HTTPReplicationSource) jsonBody(ctx context.Context, method string, query url.Values, request []byte, value any) error {
	response, err := c.requestBody(ctx, method, query, "application/json", request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	limit := api.ObjectNodeReplicationRecordMaxBytes
	if query.Get(ReplicationQuery) == "entries" {
		limit = api.ObjectNodeReplicationBatchMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil {
		return err
	}
	if len(body) > limit {
		return ErrCapacity
	}
	if entries, ok := value.(*[]JournalEntry); ok {
		err = decodeReplicationBatch(body, entries)
	} else {
		err = decodeBackupRecord(body, value)
	}
	if err == nil && query.Get(ReplicationQuery) == "status" {
		c.batch.Store(response.Header.Get(ReplicationBatchHeader) == "1")
	}
	return err
}

func (c *HTTPReplicationSource) EntryBatchLimit() int {
	if c.batch.Load() {
		return api.ObjectNodeReplicationBatch
	}
	return 1
}

func (c *HTTPReplicationSource) Entries(ctx context.Context, stream string, sequence int64, limit int) ([]JournalEntry, error) {
	if !validReceiptID(stream) || sequence < 1 || sequence > api.ObjectNodeReplicationMaxSequence || limit < 1 || limit > api.ObjectNodeReplicationBatch || int64(limit-1) > api.ObjectNodeReplicationMaxSequence-sequence {
		return nil, ErrInvalid
	}
	var entries []JournalEntry
	if !c.batch.Load() {
		// A missing advertisement keeps old sources usable without probing or
		// downgrading after a failed/corrupt batch response.
		e, err := c.Entry(ctx, stream, sequence)
		return []JournalEntry{e}, err
	}
	err := c.json(ctx, url.Values{ReplicationQuery: {"entries"}, "stream": {stream}, "sequence": {strconv.FormatInt(sequence, 10)}, "limit": {strconv.Itoa(limit)}}, &entries)
	if err == nil {
		if len(entries) < 1 || len(entries) > limit {
			return nil, ErrReplicationFork
		}
		for n, e := range entries {
			if !validJournalEntry(e) || e.StreamID != stream || e.Sequence != sequence+int64(n) || n > 0 && e.Previous != entries[n-1].Hash {
				return nil, ErrReplicationFork
			}
		}
	}
	return entries, err
}

func (c *HTTPReplicationSource) Confirm(ctx context.Context, confirmation ReplicaConfirmation) (ReplicaDurability, error) {
	var result ReplicaDurability
	if !validReplicaConfirmation(confirmation) {
		return result, ErrInvalid
	}
	body, err := json.Marshal(confirmation)
	if err != nil || len(body) > api.ObjectNodeReplicaAckMaxBytes {
		return result, ErrInvalid
	}
	err = c.jsonBody(ctx, http.MethodPost, url.Values{ReplicationQuery: {"confirm"}}, body, &result)
	if err == nil && (result.Version != 1 || result.Mode != DurabilityReplica || result.PolicyID != confirmation.PolicyID || result.ReplicaID != confirmation.ReplicaID || result.ConfirmedSequence != confirmation.Sequence || result.ConfirmedHash != confirmation.Hash || result.ConfirmedAt.IsZero()) {
		err = ErrReplicationFork
	}
	return result, err
}

func (c *HTTPReplicationSource) Fence(ctx context.Context, r FenceRequest) (PrimaryHandoff, error) {
	var handoff PrimaryHandoff
	if !validReceiptID(r.StreamID) || !validReceiptID(r.PrimaryID) || !validReceiptID(r.TargetID) || r.PrimaryID == r.TargetID || r.Epoch < 1 || r.Epoch >= api.ObjectNodeReplicationMaxEpoch {
		return handoff, ErrInvalid
	}
	err := c.jsonRequest(ctx, http.MethodPost, url.Values{ReplicationQuery: {"fence"}, "stream": {r.StreamID}, "primary": {r.PrimaryID}, "target": {r.TargetID}, "epoch": {strconv.FormatInt(r.Epoch, 10)}}, &handoff)
	if err == nil && (!validHandoff(handoff) || handoff.StreamID != r.StreamID || handoff.PrimaryID != r.PrimaryID || handoff.TargetID != r.TargetID || handoff.Epoch != r.Epoch) {
		err = ErrReplicationFork
	}
	return handoff, err
}

func (c *HTTPReplicationSource) Head(ctx context.Context) (ReplicationStatus, error) {
	var status ReplicationStatus
	err := c.json(ctx, url.Values{ReplicationQuery: {"status"}}, &status)
	if err == nil && !validReplicationStatus(status) {
		err = ErrCorrupt
	}
	return status, err
}

func (c *HTTPReplicationSource) WaitForChange(ctx context.Context, cursor ReplicationWaitCursor) (ReplicationStatus, error) {
	var status ReplicationStatus
	if !validReplicationWaitCursor(cursor) {
		return status, ErrInvalid
	}
	err := c.json(ctx, url.Values{ReplicationQuery: {"wait"}, "stream": {cursor.StreamID}, "primary": {cursor.PrimaryID}, "epoch": {strconv.FormatInt(cursor.Epoch, 10)}, "sequence": {strconv.FormatInt(cursor.Sequence, 10)}, "hash": {cursor.Hash}, "revision": {cursor.Revision}}, &status)
	if err != nil {
		return status, err
	}
	if !validReplicationStatus(status) {
		return status, ErrCorrupt
	}
	if status.Role == "follower" || status.StreamID != cursor.StreamID || status.PrimaryID != cursor.PrimaryID || status.Epoch != cursor.Epoch || status.AppliedSequence < cursor.Sequence || status.AppliedSequence == cursor.Sequence && status.AppliedHash != cursor.Hash {
		return status, ErrReplicationFork
	}
	if status.BaseSequence > cursor.Sequence {
		return status, ErrReplicaReseed
	}
	return status, nil
}

func (c *HTTPReplicationSource) Entry(ctx context.Context, stream string, sequence int64) (JournalEntry, error) {
	var entry JournalEntry
	if !validReceiptID(stream) || sequence < 1 || sequence > api.ObjectNodeReplicationMaxSequence {
		return entry, ErrInvalid
	}
	err := c.json(ctx, url.Values{ReplicationQuery: {"entry"}, "stream": {stream}, "sequence": {strconv.FormatInt(sequence, 10)}}, &entry)
	if err == nil && (!validJournalEntry(entry) || entry.StreamID != stream || entry.Sequence != sequence) {
		err = ErrReplicationFork
	}
	return entry, err
}

func (c *HTTPReplicationSource) Blob(ctx context.Context, stream, digest string, offset, length int64) (io.ReadCloser, error) {
	if !validReceiptID(stream) || !validScrubFindingID(digest) || offset < 0 || length < 1 || length > api.ObjectNodeReplicationChunkBytes {
		return nil, ErrInvalid
	}
	response, err := c.request(ctx, http.MethodGet, url.Values{ReplicationQuery: {"blob"}, "stream": {stream}, "sha256": {digest}, "offset": {strconv.FormatInt(offset, 10)}, "length": {strconv.FormatInt(length, 10)}}, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	if response.ContentLength != length || response.Header.Get("X-Gregale-Replication-Sha256") != digest || response.Header.Get("X-Gregale-Replication-Offset") != strconv.FormatInt(offset, 10) {
		_ = response.Body.Close()
		return nil, errors.New("object replication: invalid blob range response")
	}
	return response.Body, nil
}

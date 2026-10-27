package agentd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Bucket deliveries (ADR-20261027090010 §8) write a run's output files to one of the
// account's Gregale buckets, at <prefix><run id>/<name>. Each file goes
// through the S3 gateway on a one-shot upload grant: the grant authorizes
// exactly one PUT of that key and size, no credential leaves the platform,
// and the gateway applies the bucket's quotas and write fences as for any
// other write. Rewriting a file with the same bytes is harmless, so an
// attempt whose answer was lost is simply made again.

// uploadGrantTTL bounds one grant; an attempt uses it at once.
const uploadGrantTTL = 300

// BucketGrants resolves a bucket and authorizes one PUT into it;
// *state.PgStore implements it.
type BucketGrants interface {
	GetObjectBucket(ctx context.Context, accountID, appID, id string) (state.ObjectBucket, error)
	CreateObjectUploadGrant(ctx context.Context, g state.ObjectUploadGrant, ttl int) (state.ObjectUploadGrant, error)
}

// Buckets writes bucket deliveries.
type Buckets struct {
	Grants BucketGrants
	// Files is the artifact store holding the runs' output files.
	Files storage.StorageBackend
	// Endpoint is the S3 gateway's https origin, such as
	// https://s3.gregale.dev.
	Endpoint string
	HTTP     *http.Client
}

func (b *Buckets) deliver(ctx context.Context, c agentstore.DeliveryClaim) agentstore.DeliveryResult {
	bucket, err := b.Grants.GetObjectBucket(ctx, c.AccountID, c.BucketAppID, c.BucketID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return rejected("bucket_not_found", "the bucket no longer exists")
	case err != nil:
		return retry("bucket_unavailable", "the bucket could not be read")
	case bucket.State != "ready":
		return retry("bucket_not_ready", "the bucket is "+bucket.State)
	}
	for _, f := range c.Files {
		if result, ok := b.put(ctx, c, bucket, f); !ok {
			return result
		}
	}
	return agentstore.DeliveryResult{Outcome: agentstore.DeliveryDelivered, Code: "delivered",
		URL: "s3://" + bucket.Name + "/" + c.Prefix + c.RunID + "/"}
}

// put writes one file; ok is false with the attempt's result when it did not.
func (b *Buckets) put(ctx context.Context, c agentstore.DeliveryClaim, bucket state.ObjectBucket, f agentstore.OutputFile) (agentstore.DeliveryResult, bool) {
	content, err := b.read(ctx, f)
	if err != nil {
		return retry("output_unavailable", err.Error()), false
	}
	key := c.Prefix + c.RunID + "/" + f.Name
	token, hash, err := s3gateway.GenerateUploadGrantToken()
	if err != nil {
		return retry("grant_unavailable", "an upload grant could not be made"), false
	}
	headers := map[string]string{"Content-Length": strconv.Itoa(len(content)), "Content-Type": "application/octet-stream",
		"X-Amz-Meta-Gregale-Agent-Run": c.RunID, "X-Amz-Meta-Sha256": strings.TrimPrefix(f.SHA256, "sha256:")}
	_, err = b.Grants.CreateObjectUploadGrant(ctx, state.ObjectUploadGrant{ID: uuid.NewString(), TokenHash: hash,
		Kind: state.ObjectUploadGrantPut, Key: key, Bucket: bucket, SizeBytes: int64(len(content)), Headers: headers}, uploadGrantTTL)
	switch {
	case errors.Is(err, state.ErrObjectBucketWriteFenced):
		return retry("bucket_write_fenced", "writes to the bucket are paused"), false
	case errors.Is(err, state.ErrInvalidArgument):
		return rejected("invalid_object", fmt.Sprintf("%s cannot be written as an object", key)), false
	case err != nil:
		return retry("grant_unavailable", "an upload grant could not be made"), false
	}
	target, err := s3gateway.UploadGrantURL(b.Endpoint, bucket.Name, key, token)
	if err != nil {
		return rejected("gateway_misconfigured", "agentd has no valid S3 gateway endpoint"), false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(content))
	if err != nil {
		return rejected("invalid_object", fmt.Sprintf("%s cannot be written as an object", key)), false
	}
	for name, value := range headers {
		if name != "Content-Length" {
			req.Header.Set(name, value)
		}
	}
	req.ContentLength = int64(len(content))
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return agentstore.DeliveryResult{Outcome: agentstore.DeliveryUnknown, Code: "bucket_outcome_unknown",
			Message: "the S3 gateway's answer was lost"}, false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return agentstore.DeliveryResult{}, true
	case code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable:
		return retry("bucket_unavailable", fmt.Sprintf("the S3 gateway answered %d", code)), false
	case code >= 500:
		return agentstore.DeliveryResult{Outcome: agentstore.DeliveryUnknown, Code: "bucket_outcome_unknown",
			Message: fmt.Sprintf("the S3 gateway answered %d", code)}, false
	default:
		return rejected("bucket_rejected", fmt.Sprintf("the S3 gateway refused %s with %d", key, code)), false
	}
}

// read returns a kept file's contents, checked against its manifest entry.
func (b *Buckets) read(ctx context.Context, f agentstore.OutputFile) ([]byte, error) {
	r, err := b.Files.Get(ctx, f.Key)
	if err != nil {
		return nil, fmt.Errorf("output %q could not be read", f.Name)
	}
	defer func() { _ = r.Close() }()
	content, err := io.ReadAll(io.LimitReader(r, int64(f.Size)+1))
	if err != nil {
		return nil, fmt.Errorf("output %q could not be read", f.Name)
	}
	sum := sha256.Sum256(content)
	if len(content) != f.Size || f.SHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
		return nil, fmt.Errorf("output %q does not match its manifest", f.Name)
	}
	return content, nil
}

func retry(code, message string) agentstore.DeliveryResult {
	return agentstore.DeliveryResult{Outcome: agentstore.DeliveryRetry, Code: code, Message: message}
}

func rejected(code, message string) agentstore.DeliveryResult {
	return agentstore.DeliveryResult{Outcome: agentstore.DeliveryRejected, Code: code, Message: message}
}

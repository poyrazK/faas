package s3gateway

import (
	"context"
	"net/http"
	"time"
)

// VerifyHeaderSignature verifies a nonstreaming S3 header signature for one
// explicitly selected credential. It does not resolve credentials or forward
// traffic. The caller must validate the body against the returned payload hash.
func VerifyHeaderSignature(ctx context.Context, r *http.Request, access, secret, region string, now time.Time) (string, error) {
	parsed, e := parseSigV4(r, region, now)
	if e != nil || parsed.AccessKeyID != access || isStreamingPayloadHash(parsed.PayloadHash) {
		return "", errSignature
	}
	if e = verifySigV4(ctx, r, parsed, secret, region); e != nil {
		return "", e
	}
	return parsed.PayloadHash, nil
}

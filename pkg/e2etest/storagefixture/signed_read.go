package storagefixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// SignedRead creates an independently signed fixture observation. Its marker
// excludes host observers from measurements of the guest's captured transport;
// the production gateway still validates the ordinary SigV4 credential.
func SignedRead(ctx context.Context, endpoint, bucket, key string, credential aws.Credentials) (*http.Request, error) {
	target := strings.TrimRight(endpoint, "/") + "/" + url.PathEscape(bucket)
	if key == "" {
		target += "?list-type=2&max-keys=1"
	} else {
		target += "/" + url.PathEscape(key)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(nil)
	hash := hex.EncodeToString(digest[:])
	request.Header.Set("X-Amz-Content-Sha256", hash)
	request.Header.Set("X-Gregale-Acceptance-Observer", "1")
	if err := v4.NewSigner(func(options *v4.SignerOptions) { options.DisableURIPathEscaping = true }).SignHTTP(ctx, credential, request, hash, "s3", "us-east-1", time.Now()); err != nil {
		return nil, err
	}
	return request, nil
}

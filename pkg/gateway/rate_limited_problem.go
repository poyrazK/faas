package gateway

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// rateLimitBucket is a token bucket's state at a 429: limit is its burst
// capacity (the X-*RateLimit-Limit header) and remaining its unspent tokens.
type rateLimitBucket struct {
	limit, remaining int
	ok               bool
}

// writeRateLimited answers a request-rate 429. Limit errors carry the limit,
// the observed value and a docs link (CLAUDE.md conventions, H5-10): limit is
// the bucket's burst capacity and observed is how much of it the caller has
// spent, so a client can tell which bucket tripped and how far over it is.
func writeRateLimited(w http.ResponseWriter, bucket rateLimitBucket, detail string) {
	problem := api.NewProblem(http.StatusTooManyRequests, "rate_limited", "Rate limit exceeded", detail).
		WithDocs(docsTypeBase)
	if bucket.ok && bucket.limit > 0 {
		problem = problem.WithLimit(int64(bucket.limit), int64(bucket.limit-bucket.remaining))
	}
	api.WriteProblem(w, problem)
}

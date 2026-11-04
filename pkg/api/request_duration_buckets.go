package api

// RequestDurationTopBucketSeconds is the largest finite bucket of
// gateway_request_duration_seconds and its per-deployment sibling. A latency
// quantile at this value means "at least this long": histogram_quantile
// cannot see past the top finite bucket (Hunt 12: 12 s requests read as
// p95=10000.0ms).
const RequestDurationTopBucketSeconds = 10.0

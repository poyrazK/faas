package api

// Job task log limits are part of the public jobs API contract. Keep the
// defaults here so the server, CLI, and Go SDK use the same range.
const (
	// DefaultJobTaskLogMaxBytes is the compact tail returned when callers do
	// not request a custom log size.
	DefaultJobTaskLogMaxBytes = 64 * 1024
	// MaxJobTaskLogMaxBytes is the largest tail the API will return in one
	// response.
	MaxJobTaskLogMaxBytes = 1024 * 1024
)

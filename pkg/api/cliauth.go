package api

// CLI auth device-code flow (spec §2.2). The CLI mints a code
// anonymously, the user pastes it into the browser at /cli-auth, the
// dashboard binds it to an account (creating one if needed), and the
// CLI polls for the plaintext API key. The shapes here are the wire
// contract; both `cmd/apid` and `cmd/faas` import them so the two
// sides never disagree.

// CliAuthStatus is the lifecycle state of a /v1/cli-auth/code row.
type CliAuthStatus string

const (
	CliAuthStatusPending  CliAuthStatus = "pending"
	CliAuthStatusConsumed CliAuthStatus = "consumed"
	CliAuthStatusExpired  CliAuthStatus = "expired"
)

// CliAuthCodeResponse is what POST /v1/cli-auth/code returns. The CLI
// prints the URL and accepts the code as a fallback for browser-less
// machines (paste-the-code-into-the-same-terminal mode).
type CliAuthCodeResponse struct {
	Code      string `json:"code"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"` // RFC3339
}

// CliAuthExchangeRequest is the CLI's poll body.
type CliAuthExchangeRequest struct {
	Code string `json:"code"`
}

// CliAuthExchangeResponse is the only response the CLI writes to disk.
// Plaintext is shown exactly once and never persisted server-side.
type CliAuthExchangeResponse struct {
	Plaintext string         `json:"plaintext"`
	Account   AccountResponse `json:"account"`
}
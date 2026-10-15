// Package apptaskattach holds apid's half of interactive app tasks
// (ADR-1037): the boot-time gate, the one-time attach credential and the
// response that hands it to the client. apid stores only the credential's
// SHA-256 digest; vmmd matches the digest when the client attaches.
package apptaskattach

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// EnabledFromEnv reads the operator gate that admits interactive sessions on
// top of the app-task API gate.
func EnabledFromEnv(getenv func(string) string) bool {
	return strings.TrimSpace(getenv("FAAS_INTERACTIVE_APP_TASKS")) == "1"
}

// Mint returns a fresh attach token for an interactive task, or the problem
// to answer with when interactive sessions are not enabled.
func Mint(enabled bool) (string, *api.Problem) {
	if !enabled {
		return "", api.NewProblem(http.StatusNotImplemented, api.CodeNotImplemented,
			"Interactive sessions unavailable", "interactive app tasks are not enabled on this control-plane host")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", api.ErrInternal("could not mint an attach token")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Session is the record admission persists for an interactive task: the
// token's digest, never the token. It is nil for an ordinary task.
func Session(interactive, tty bool, token string) (*state.AppTaskInteractive, error) {
	if interactive != (token != "") {
		return nil, errors.New("apptaskattach: interactive admission requires exactly one attach token")
	}
	if token == "" {
		return nil, nil
	}
	digest := sha256.Sum256([]byte(token))
	return &state.AppTaskInteractive{TTY: tty, TokenSHA256: digest[:]}, nil
}

// Respond adds the one-time credential to a create response. The token is
// shown once, so the response must never be cached or stored for
// Idempotency-Key replay.
func Respond(w http.ResponseWriter, resp *api.AppTaskResponse, slug, token string) {
	if token == "" {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.SkipIdempotentReplay(w)
	resp.Interactive = true
	resp.Attach = &api.AppTaskAttachInfo{
		Token: token, Path: api.AppTaskAttachPath(slug, resp.ID), Subprotocol: api.AppTaskAttachSubprotocol,
	}
}

// RejectExclusive answers an exclusive operation that names an interactive
// task: those are admitted only by POST /v1/apps/{slug}/tasks.
func RejectExclusive() *api.Problem {
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid task selector",
		"interactive sessions are admitted only by POST /v1/apps/{slug}/tasks")
}

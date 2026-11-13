package apidserver

// One-click CLI login (ADR-20261111170000). `gregale login` listens on
// 127.0.0.1:<port> and mints its code with that port. The approval page then
// needs only the Authorize button: approving claims the code and replaces its
// device code with a fresh one in the same statement, and the browser carries
// the new device code to http://127.0.0.1:<port>/callback on its own machine.
// The terminal that minted the code holds only the old device code, so a
// login link someone sends still cannot authorize their terminal (the
// device-code phishing the typed code guarded against). A terminal on
// another machine (SSH, headless) uses the typed-code flow instead.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// cliAuthLoopbackPort is the one-click callback port for a login code, or 0
// when the page uses the typed-code flow: no or a malformed port, or a code
// that is not a device-bound login code.
func cliAuthLoopbackPort(rawPort, rawCode string) int {
	if rawPort == "" || isMCPAuthCode(rawCode) || !isShortCliAuthCode(rawCode) {
		return 0
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || !validLoopbackPort(port) {
		return 0
	}
	return port
}

// cliAuthLoopbackURL is the CLI's callback for a one-click approval. The
// host is fixed: only the port comes from the request.
func cliAuthLoopbackURL(port int, rawCode, deviceCode string) string {
	q := url.Values{}
	q.Set("code", rawCode)
	q.Set("device_code", deviceCode)
	return "http://127.0.0.1:" + strconv.Itoa(port) + api.CliAuthLoopbackCallbackPath + "?" + q.Encode()
}

// approveLoopbackCode claims a one-click code for accountID under a new
// device code and sends the browser to the CLI's loopback callback with it.
// The page navigates by script (with a link as fallback) rather than a
// redirect: CSP form-action 'self' blocks a form POST redirecting to
// another origin.
func (h *cliAuthHandlers) approveLoopbackCode(w http.ResponseWriter, r *http.Request, accountID, rawCode string, hash []byte, port int) {
	devices, ok := h.srv.store.(state.CliAuthDeviceCodeStore)
	if !ok {
		h.renderCliAuthError(w, r, "Sign-in unavailable", "Run 'gregale login --device' to sign in with a code.")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		h.log.Error("cli_auth.loopback_device", "err", err)
		renderProblem(w, h.log, err)
		return
	}
	if err := devices.ClaimCliAuthCodeForDevice(r.Context(), hash, accountID, api.HashToken(raw)); err != nil {
		switch {
		case errors.Is(err, state.ErrConflict):
			h.renderCliAuthError(w, r, "Code already used", "Restart 'gregale login' to sign in again.")
		case errors.Is(err, state.ErrNotFound):
			h.renderCliAuthError(w, r, "Code expired", "Restart 'gregale login' to sign in again.")
		default:
			h.log.Error("cli_auth.loopback_claim", "err", err)
			api.WriteProblemForRequest(w, r, api.ErrInternal(
				"Gregale could not authorize this CLI session.",
			).WithHint("Restart `gregale login`."))
		}
		return
	}
	_ = h.srv.notif.Notify(r.Context(), db.NotifyCliAuthCodeActivated,
		`{"hash":"`+hex.EncodeToString(hash)+`"}`)
	// The device code must not linger in a cache or leak in a Referer.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	page := dashboard.Page{
		Title: "Finishing sign-in",
		Body:  "cli_auth",
		Data:  map[string]any{"LoopbackURL": cliAuthLoopbackURL(port, rawCode, hex.EncodeToString(raw))},
	}
	if err := dashboard.Render(w, h.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		h.log.Error("cli_auth.loopback_render", "err", err)
		renderProblem(w, h.log, err)
	}
}

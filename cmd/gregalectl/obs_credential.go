package main

import (
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/sessioncookie"
	"net/http"
	"os"
	"strings"
)

// obsAdminTokenUsage is the --admin-token help shared by the obs reads.
const obsAdminTokenUsage = "admin bearer (default: the 'gregalectl auth login' session, then $FAAS_ADMIN_TOKEN)"

// obsCredential authenticates one operator obs read: the MFA-completed
// operator session from 'gregalectl auth login', or an admin bearer.
type obsCredential struct {
	sessionCookie string
	bearer        string
}

// resolveObsCredential picks an explicit --admin-token first, so a one-off
// command can still name a token; then the operator session, so a logged-in
// operator reads with MFA even with $FAAS_ADMIN_TOKEN exported; then
// $FAAS_ADMIN_TOKEN, for automation. When none is usable it reports why on
// stderr and returns false.
func resolveObsCredential(command, flagToken string) (obsCredential, bool) {
	if token := strings.TrimSpace(flagToken); token != "" {
		return obsCredential{bearer: token}, true
	}
	sess, sessErr := loadOperatorSession()
	if sessErr == nil {
		return obsCredential{sessionCookie: sess.Cookie}, true
	}
	if token := strings.TrimSpace(os.Getenv("FAAS_ADMIN_TOKEN")); token != "" {
		return obsCredential{bearer: token}, true
	}
	reason := sessErr.Error()
	if errors.Is(sessErr, os.ErrNotExist) {
		reason = "no operator session; run 'gregalectl auth login --email <email>'"
	}
	_, _ = fmt.Fprintf(osStderr, "gregalectl %s: %s, or pass --admin-token (or $FAAS_ADMIN_TOKEN)\n", command, reason)
	return obsCredential{}, false
}

// apply authenticates req. A session request carries the CLI User-Agent
// the session was issued to, as every other operator session call does.
func (c obsCredential) apply(req *http.Request) {
	if c.sessionCookie != "" {
		sessioncookie.AddToRequest(req, c.sessionCookie)
		req.Header.Set("User-Agent", operatorUserAgent)
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)
}

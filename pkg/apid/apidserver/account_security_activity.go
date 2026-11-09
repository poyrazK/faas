package apidserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Security activity (ADR-20261108150000): the account's own sign-ins,
// credentials and security changes, read from audit_log. Only kinds in
// securityActivityKinds are shown, each with a fixed phrase, so nothing a
// client sent is echoed as prose.

var securityActivityKinds = map[string]string{
	"auth.session.created":                     "Signed in to the dashboard",
	"auth.login":                               "Signed in from the CLI or API",
	"auth.logout":                              "Signed out",
	"auth.session.revoke":                      "A browser session was signed out",
	"auth.sessions.revoke_all":                 "All browser sessions were signed out",
	"auth.sessions_revoked_on_password_change": "Sessions signed out after a password change",
	"auth.sign_in_refused_unverified":          "Sign-in refused: email address not verified",
	"auth.oauth_unlinked_refused":              "Sign-in refused: that provider was removed",
	"auth.mfa.recovery_codes_regenerated":      "Recovery codes replaced",
	"account.password_set":                     "Password set or changed",
	"account.email_change_requested":           "Email change requested",
	"account.email_changed":                    "Sign-in email changed",
	"account.mfa_enrolled":                     "Two-factor authentication set up",
	"account.mfa_confirmed":                    "Two-factor authentication confirmed",
	"account.mfa_disabled":                     "Two-factor authentication turned off",
	"account.mfa_recovered":                    "Signed in with a recovery code",
	"account.mfa_browser_remembered":           "Browser remembered for two-factor sign-in",
	"auth.mfa_trusted_browser_used":            "Signed in from a remembered browser",
	"account.mfa_browser_forgotten":            "A remembered browser was forgotten",
	"account.mfa_browsers_forgotten":           "Remembered browsers were forgotten",
	"account.sign_in_method_linked":            "Sign-in method linked",
	"account.sign_in_method_unlinked":          "Sign-in method removed",
	"account.sign_in_method_link_refused":      "Linking a sign-in method was refused",
	"account.deletion_scheduled":               "Account deletion scheduled",
	"account.deletion_restored":                "Account deletion cancelled",
	"api_key.created":                          "API key created",
	"api_key.rotated":                          "API key rotated",
	"api_key.revoked":                          "API key revoked",
}

// securityActivityPrefixes are the audit kind prefixes the allowlist spans;
// one read per prefix keeps each on the received_at index.
var securityActivityPrefixes = []string{"auth.", "account.", "api_key."}

// getSecurityActivity is GET /v1/account/security-activity.
func (s *server) getSecurityActivity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	events, err := s.securityActivity(r.Context(), acct.ID, time.Now())
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "list security activity",
			"Gregale could not load your security activity.",
			"Try again in a moment; if it continues, contact support.", err)
		return
	}
	writeJSON(w, http.StatusOK, api.SecurityActivityResponse{Events: events})
}

func (s *server) securityActivity(ctx context.Context, accountID string, now time.Time) ([]api.SecurityActivityEvent, error) {
	id, err := uuid.Parse(accountID)
	if err != nil {
		return nil, fmt.Errorf("parse account id: %w", err)
	}
	var rows []state.AuditLog
	for _, prefix := range securityActivityPrefixes {
		got, err := s.store.ListAuditLog(ctx, state.AuditLogFilter{
			AccountID:  &id,
			KindPrefix: prefix,
			Since:      now.Add(-api.SecurityActivityLookback),
			Limit:      listAuditLogOverRead,
		})
		if err != nil {
			return nil, fmt.Errorf("list %s audit rows: %w", prefix, err)
		}
		rows = append(rows, got...)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ReceivedAt.After(rows[j].ReceivedAt) })
	out := make([]api.SecurityActivityEvent, 0, min(len(rows), api.SecurityActivityMaxEvents))
	for _, row := range rows {
		summary, ok := securityActivityKinds[row.Kind]
		if !ok {
			continue
		}
		ip, detail := securityActivityFields(row.Data)
		out = append(out, api.SecurityActivityEvent{
			At: row.ReceivedAt.UTC().Format(time.RFC3339), Kind: row.Kind, Summary: summary, IP: ip, Detail: detail,
		})
		if len(out) == api.SecurityActivityMaxEvents {
			break
		}
	}
	return out, nil
}

// securityActivityFields pulls the client address and one short qualifier
// (sign-in method, provider or key label) out of an audit payload.
func securityActivityFields(raw json.RawMessage) (ip, detail string) {
	var data map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &data) != nil {
		return "", ""
	}
	for _, k := range []string{"issued_ip", "created_ip", "ip"} {
		if v, ok := data[k].(string); ok && v != "" {
			ip = v
			break
		}
	}
	for _, k := range []string{"method", "provider", "label", "ua_family"} {
		if v, ok := data[k].(string); ok && v != "" {
			detail = v
			break
		}
	}
	return securityActivityClean(ip, 64), securityActivityClean(detail, 64)
}

func securityActivityClean(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > limit {
		s = string(r[:limit])
	}
	return s
}

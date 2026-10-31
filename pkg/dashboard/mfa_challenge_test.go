package dashboard_test

import (
	"html"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dashboard"
)

// The TOTP challenge tells a reused code apart from a wrong one: a code
// that already verified a session needs the next code, not a retype.
func TestMFAChallengeExplainsAReusedCode(t *testing.T) {
	const mismatch, reused = "That code did not match.", "That code was already used."
	for _, tc := range []struct {
		name       string
		data       dashboard.MFAChallengeData
		want, omit []string
	}{
		{"first try", dashboard.MFAChallengeData{Enrolled: true}, nil, []string{mismatch, reused}},
		{"wrong code", dashboard.MFAChallengeData{Enrolled: true, Failed: true}, []string{mismatch}, []string{reused}},
		{"reused code", dashboard.MFAChallengeData{Enrolled: true, Failed: true, Reused: true}, []string{reused, "show the next code"}, []string{mismatch}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			page := dashboard.Page{Title: "Two-factor authentication", Account: &dashboard.AccountView{Email: "operator@gregale.test"}, Body: "mfa", Data: tc.data}
			if err := dashboard.Render(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), "", page); err != nil {
				t.Fatalf("render: %v", err)
			}
			body := html.UnescapeString(rec.Body.String())
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("page lacks %q", want)
				}
			}
			for _, omit := range tc.omit {
				if strings.Contains(body, omit) {
					t.Errorf("page shows %q", omit)
				}
			}
		})
	}
}

package devbridge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSessionIsolation(t *testing.T) {
	now := time.Now()
	s, c, err := NewSession(Scope{AccountID: "a", DeveloperID: "d", EnvironmentID: "dev", TargetAppID: "payments", DependencyAppIDs: []string{"orders"}}, now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeRequest(now, c.RequestToken, "a", "dev", "payments"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ token, account, env, app string }{
		{c.AttachmentToken, "a", "dev", "payments"},
		{c.RequestToken, "other", "dev", "payments"},
		{c.RequestToken, "a", "production", "payments"},
		{c.RequestToken, "a", "dev", "orders"},
		{"forged", "a", "dev", "payments"},
	} {
		if s.AuthorizeRequest(now, test.token, test.account, test.env, test.app) == nil {
			t.Fatalf("authorized invalid scope: %#v", test)
		}
	}
	if s.AuthorizeAttachment(now, c.RequestToken) == nil {
		t.Fatal("request token attached laptop")
	}
	if err := s.AuthorizeDependency(now, c.AttachmentToken, "a", "dev", "orders"); err != nil {
		t.Fatal(err)
	}
	if s.AuthorizeDependency(now, c.AttachmentToken, "a", "dev", "database") == nil {
		t.Fatal("undeclared dependency authorized")
	}
	if s.AuthorizeAttachment(s.ExpiresAt, c.AttachmentToken) == nil {
		t.Fatal("expired session authorized")
	}
	s.RevokedAt = &now
	if s.AuthorizeAttachment(now, c.AttachmentToken) == nil {
		t.Fatal("revoked session authorized")
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), c.RequestToken) || strings.Contains(string(b), c.AttachmentToken) || strings.Contains(string(b), "Digest") {
		t.Fatal("credentials leaked in session response")
	}
}

package projectsetup

import (
	"strings"
	"testing"
)

// A bare host:port value (gRPC *_ADDR) is named in the dry-run note; it read
// "imported with  as its address" because url.Parse takes the host for a
// scheme.
func TestRewrittenEnvNotesNameBareHostPort(t *testing.T) {
	plan := EnvImport{
		Values:    map[string]string{"CART_ADDR": "cartservice.svc.gregale:7070", "API_URL": "http://api.svc.gregale:8080/v1"},
		Rewritten: map[string]string{"CART_ADDR": "cartservice", "API_URL": "api"},
		Plaintext: map[string]bool{"API_URL": true},
	}
	notes := RewrittenEnvNotes("frontend", []string{"CART_ADDR", "API_URL"}, plan)
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "CART_ADDR calls cartservice; imported with cartservice.svc.gregale:7070 as its address") {
		t.Fatalf("notes = %q", notes)
	}
	if !strings.Contains(joined, "API_URL calls api; imported with api.svc.gregale:8080 as its address over http") {
		t.Fatalf("notes = %q", notes)
	}
}

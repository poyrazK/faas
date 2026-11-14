package disposableemail_test

import (
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/disposableemail"
)

func TestDomain(t *testing.T) {
	for _, tc := range []struct {
		domain string
		want   bool
	}{
		{"mailinator.com", true},
		{"MAILINATOR.com.", true},
		{"sub.mailinator.com", true},
		{"gmail.com", false},
		{"gregale.dev", false},
		{"mailinator.com.gregale.dev", false},
		{"", false},
	} {
		if got := disposableemail.Domain(tc.domain); got != tc.want {
			t.Errorf("Domain(%q) = %v, want %v", tc.domain, got, tc.want)
		}
	}
}

func TestEmailDomain(t *testing.T) {
	for in, want := range map[string]string{
		"a@Example.COM": "example.com",
		"a@b@c.io":      "c.io",
		"no-at":         "",
		"trailing@":     "",
	} {
		if got := disposableemail.EmailDomain(in); got != want {
			t.Errorf("EmailDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

// The embedded list is a copy of the auth template's; a refresh must update both.
func TestListMatchesAuthTemplate(t *testing.T) {
	ours, err := os.ReadFile("domains.txt")
	if err != nil {
		t.Fatal(err)
	}
	template, err := os.ReadFile("../../cmd/gregale/templates/auth/disposable-domains.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(ours) != string(template) {
		t.Fatal("pkg/disposableemail/domains.txt differs from cmd/gregale/templates/auth/disposable-domains.txt; copy the template's list")
	}
}

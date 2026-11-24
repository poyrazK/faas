package api

import (
	"strings"
	"testing"
)

func TestParseAppNameBlocklist(t *testing.T) {
	got, err := ParseAppNameBlocklist(" PayPal, *-login\n*microsoft*  paypal\t")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "paypal,*-login,*microsoft*" {
		t.Fatalf("got %v", got)
	}
	if got, err := ParseAppNameBlocklist(""); err != nil || len(got) != 0 {
		t.Fatalf("empty: got %v, %v", got, err)
	}
	for _, bad := range []string{"pay_pal", "*", "-*-", "a/b", "[a]", "a?b", strings.Repeat("a", 64)} {
		if _, err := ParseAppNameBlocklist(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestMatchAppNameBlocklist(t *testing.T) {
	list := []string{"paypal", "*-login", "secure-*", "*microsoft*"}
	for slug, want := range map[string]string{
		"paypal":              "paypal",
		"paypal-shop":         "",
		"bank-login":          "*-login",
		"login":               "",
		"secure-pay":          "secure-*",
		"my-microsoft-office": "*microsoft*",
		"microsoft":           "*microsoft*",
		"shop":                "",
	} {
		entry, ok := MatchAppNameBlocklist(slug, list)
		if ok != (want != "") || entry != want {
			t.Errorf("%s: got %q, %v; want %q", slug, entry, ok, want)
		}
	}
}

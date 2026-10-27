// adr: 20261026112500
package sessioncookie

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetPrefersTheHostCookie(t *testing.T) {
	for name, tc := range map[string]struct {
		cookies []*http.Cookie
		want    string
		ok      bool
	}{
		"host only":   {[]*http.Cookie{{Name: Name, Value: "real"}}, "real", true},
		"legacy only": {[]*http.Cookie{{Name: LegacyName, Value: "old"}}, "old", true},
		// A tenant page on another host can set faas_sid with
		// Domain=<registrable domain>; it never replaces the __Host- one.
		"shadowed":           {[]*http.Cookie{{Name: LegacyName, Value: "attacker"}, {Name: Name, Value: "real"}}, "real", true},
		"empty host cookie":  {[]*http.Cookie{{Name: Name, Value: ""}, {Name: LegacyName, Value: "old"}}, "old", true},
		"none":               {nil, "", false},
		"other cookies only": {[]*http.Cookie{{Name: "faas_csrf", Value: "x"}}, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, c := range tc.cookies {
				r.AddCookie(c)
			}
			c, err := Get(r)
			if (err == nil) != tc.ok || (err == nil && c.Value != tc.want) {
				t.Fatalf("Get = %v, %v; want %q ok=%v", c, err, tc.want, tc.ok)
			}
		})
	}
}

func TestSetWritesTheHostCookieOverHTTPS(t *testing.T) {
	rec := httptest.NewRecorder()
	Set(rec, http.Cookie{Name: "ignored", Value: "v", Path: "/dashboard", Domain: "gregale.dev", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 60})
	got := rec.Result().Cookies()
	if len(got) != 2 {
		t.Fatalf("cookies = %v, want the session and the legacy expiry", got)
	}
	host, legacy := got[0], got[1]
	if host.Name != Name || host.Value != "v" || host.Path != "/" || host.Domain != "" || !host.Secure || !host.HttpOnly || host.MaxAge != 60 {
		t.Fatalf("session cookie = %+v, want a __Host- cookie (Secure, Path=/, no Domain)", host)
	}
	if legacy.Name != LegacyName || legacy.Value != "" || legacy.MaxAge != -1 {
		t.Fatalf("legacy cookie = %+v, want it expired", legacy)
	}
	header := strings.Join(rec.Result().Header.Values("Set-Cookie"), "\n")
	if strings.Contains(strings.SplitN(header, "\n", 2)[0], "Domain=") {
		t.Fatalf("__Host- Set-Cookie carries a Domain: %s", header)
	}
}

// A plain-HTTP install cannot hold a __Host- cookie; it keeps the legacy name.
func TestSetKeepsTheLegacyNameWithoutHTTPS(t *testing.T) {
	rec := httptest.NewRecorder()
	Set(rec, http.Cookie{Value: "v", Path: "/", HttpOnly: true})
	got := rec.Result().Cookies()
	if len(got) != 1 || got[0].Name != LegacyName || got[0].Value != "v" {
		t.Fatalf("cookies = %v, want one legacy cookie", got)
	}
}

func TestClearExpiresBoth(t *testing.T) {
	rec := httptest.NewRecorder()
	Clear(rec, "")
	got := rec.Result().Cookies()
	if len(got) != 2 || got[0].Name != Name || got[1].Name != LegacyName || got[0].MaxAge != -1 || got[1].MaxAge != -1 || !got[0].Secure || got[0].Path != "/" {
		t.Fatalf("cookies = %+v", got)
	}
}

func TestClientHelpers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	AddToRequest(req, "v")
	if c, err := req.Cookie(Name); err != nil || c.Value != "v" {
		t.Fatalf("host cookie = %v %v", c, err)
	}
	if c, err := req.Cookie(LegacyName); err != nil || c.Value != "v" {
		t.Fatalf("legacy cookie = %v %v", c, err)
	}
	for name, tc := range map[string]struct {
		setCookies []string
		want       string
		ok         bool
	}{
		"new server":   {[]string{Name + "=new; Path=/; Secure", LegacyName + "=; Max-Age=0"}, "new", true},
		"old server":   {[]string{LegacyName + "=old; Path=/"}, "old", true},
		"logout":       {[]string{Name + "=; Max-Age=0", LegacyName + "=; Max-Age=0"}, "", true},
		"no session":   {[]string{"faas_csrf=x"}, "", false},
		"empty header": {nil, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{"Set-Cookie": tc.setCookies}}
			c, ok := FromResponse(resp)
			if ok != tc.ok || (ok && c.Value != tc.want) {
				t.Fatalf("FromResponse = %v %v, want %q %v", c, ok, tc.want, tc.ok)
			}
		})
	}
	if !IsName(Name) || !IsName(LegacyName) || IsName("faas_csrf") {
		t.Fatal("IsName")
	}
}

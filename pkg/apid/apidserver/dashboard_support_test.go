// adr: 20261111130000
package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"golang.org/x/net/html"
)

func TestCustomerSupportPortalBrowserRoutes(t *testing.T) {
	pool := pgtest.Open(t)
	ddl, err := os.ReadFile("../support/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	env := apidserver.Setup(t, api.PlanPro)
	acct := *env.Acct()
	srv := *env.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	if err := srv.ConfigureCustomerSupportForTest(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO accounts VALUES($1,$2,'pro','active',now(),now())`, acct.ID, acct.Email); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, form url.Values, cookies []*http.Cookie, staff bool, origin string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if staff {
			env.AddAdminSession(t, req)
		} else {
			token, err := srv.IssueDashboardSession(req, acct.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			req.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: token})
		}
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		(*env.H()).ServeHTTP(w, req)
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d want %d: %s", w.Code, status, w.Body)
		}
	}
	check(request("GET", "/dashboard/support", nil, nil, false, ""), 404)
	flag, _ := platformflags.CapabilityFlag("customer-support")
	allow := true
	srv.SeedPlatformFlag(flag, platformflags.Definition{Enabled: true, Rules: []platformflags.Rule{{ID: "portal-preview", Accounts: []string{acct.ID}, Value: &allow}}})
	page := request("GET", "/dashboard/support", nil, nil, false, "")
	check(page, 200)
	if page.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") {
		t.Fatal("portal cache/type boundary")
	}
	create := supportPortalForm(t, page, "create")
	create.Set("subject", `<script>portal attack</script>`)
	create.Set("category", "deployment")
	create.Set("priority", "normal")
	create.Set("body", "Public question")
	check(request("POST", "/dashboard/support", create, nil, false, ""), 400)
	created := request("POST", "/dashboard/support", create, page.Result().Cookies(), false, "")
	check(created, 303)
	path := strings.Split(created.Header().Get("Location"), "?")[0]
	check(request("POST", "/dashboard/support", create, page.Result().Cookies(), false, ""), 303)
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM support_tickets`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("create receipt duplicated: %d %v", count, err)
	}
	page = request("GET", path, nil, nil, false, "")
	check(page, 200)
	if strings.Contains(page.Body.String(), "<script>portal attack</script>") || !strings.Contains(page.Body.String(), "&lt;script&gt;portal attack&lt;/script&gt;") {
		t.Fatal("subject not escaped")
	}
	check(request("GET", "/dashboard/orgs/other/support", nil, nil, false, ""), 403)
	staffPath := strings.Replace(path, "/dashboard/support/", "/dashboard/admin/support/", 1)
	check(request("GET", staffPath, nil, nil, true, ""), 403)
	srv.WithAdminAllowlist(acct.Email)
	staffPage := request("GET", staffPath, nil, nil, true, "")
	check(staffPage, 200)
	note := supportPortalForm(t, staffPage, "note")
	note.Set("body", "PRIVATE-NOTE-FOR-STAFF")
	stale := request("POST", staffPath, note, staffPage.Result().Cookies(), false, "")
	check(stale, 403)
	if !strings.Contains(stale.Body.String(), "Verify your identity") || !strings.Contains(stale.Body.String(), note.Get("idempotency_key")) || !strings.Contains(stale.Body.String(), note.Get("body")) {
		t.Fatal("step-up lost draft/request key")
	}
	// The error page refreshes the CSRF cookie and its matching hidden field.
	note = supportPortalForm(t, stale, "note")
	note.Set("body", "PRIVATE-NOTE-FOR-STAFF")
	check(request("POST", staffPath, note, stale.Result().Cookies(), true, "https://evil.example"), 403)
	check(request("POST", staffPath, note, stale.Result().Cookies(), true, ""), 303)
	page = request("GET", path, nil, nil, false, "")
	check(page, 200)
	if strings.Contains(page.Body.String(), "PRIVATE-NOTE-FOR-STAFF") || strings.Contains(page.Body.String(), "Internal note") {
		t.Fatal("private note leaked into customer HTML")
	}
	staffPage = request("GET", staffPath, nil, nil, true, "")
	if !strings.Contains(staffPage.Body.String(), "PRIVATE-NOTE-FOR-STAFF") {
		t.Fatal("staff note missing")
	}
	closeForm := supportPortalForm(t, page, "close")
	check(request("POST", path, closeForm, page.Result().Cookies(), false, ""), 303)
	page = request("GET", path, nil, nil, false, "")
	rating := supportPortalForm(t, page, "satisfaction")
	rating.Set("rating", "5")
	check(request("POST", path, rating, page.Result().Cookies(), false, ""), 303)
	reopen := supportPortalForm(t, page, "reopen")
	reopen.Set("body", "The issue is still happening")
	check(request("POST", path, reopen, page.Result().Cookies(), false, ""), 303)
	if _, err := pool.Exec(t.Context(), `INSERT INTO support_staff(account_id,revision,role,profile) VALUES($1,1,'revoked','{}')`, acct.ID); err != nil {
		t.Fatal(err)
	}
	check(request("GET", staffPath, nil, nil, true, ""), 403)
}

// Read actual hidden controls so tests submit the same CSRF/key/revision as a browser.
func supportPortalForm(t *testing.T, response *httptest.ResponseRecorder, action string) url.Values {
	t.Helper()
	root, err := html.Parse(strings.NewReader(response.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	var found url.Values
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			values := url.Values{}
			var inputs func(*html.Node)
			inputs = func(node *html.Node) {
				if node.Type == html.ElementNode && node.Data == "input" {
					name, value := "", ""
					for _, attr := range node.Attr {
						if attr.Key == "name" {
							name = attr.Val
						}
						if attr.Key == "value" {
							value = attr.Val
						}
					}
					if name != "" {
						values.Set(name, value)
					}
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					inputs(child)
				}
			}
			inputs(n)
			if values.Get("action") == action {
				found = values
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if found == nil {
		t.Fatalf("form %q missing: %s", action, response.Body)
	}
	return found
}

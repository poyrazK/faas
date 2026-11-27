package api

import (
	"errors"
	"testing"
)

func TestOrgRoute(t *testing.T) {
	for _, tt := range []struct {
		name, org, method, path, want string
		refused                       bool
	}{
		{name: "personal app create", method: "POST", path: "/v1/apps", want: "/v1/apps"},
		{name: "personal plan", method: "PATCH", path: "/v1/account/plan", want: "/v1/account/plan"},
		{name: "app create", org: "acme", method: "POST", path: "/v1/apps", want: "/v1/orgs/acme/apps"},
		{name: "app list stays", org: "acme", method: "GET", path: "/v1/apps", want: "/v1/apps"},
		{name: "app-scoped stays", org: "acme", method: "POST", path: "/v1/apps/shop/deployments", want: "/v1/apps/shop/deployments"},
		{name: "bucket create", org: "acme", method: "POST", path: "/v1/apps/shop/buckets", want: "/v1/orgs/acme/apps/shop/buckets"},
		{name: "plan", org: "acme", method: "PATCH", path: "/v1/account/plan", want: "/v1/orgs/acme/plan"},
		{name: "portal", org: "acme", method: "GET", path: "/v1/billing/portal", want: "/v1/orgs/acme/billing/portal"},
		{name: "status", org: "acme", method: "GET", path: "/v1/billing/status", want: "/v1/orgs/acme/billing/status"},
		{name: "budget", org: "acme", method: "PUT", path: "/v1/billing/budgets/b1", want: "/v1/orgs/acme/billing/budgets/b1"},
		{name: "overage", org: "acme", method: "POST", path: "/v1/account/overage-cap", want: "/v1/orgs/acme/overage-cap"},
		{name: "postgres create", org: "acme", method: "POST", path: "/v1/postgres/databases", want: "/v1/orgs/acme/postgres/databases"},
		{name: "postgres query kept", org: "acme", method: "GET", path: "/v1/postgres/databases?limit=5", want: "/v1/orgs/acme/postgres/databases?limit=5"},
		{name: "project", org: "acme", method: "POST", path: "/v1/projects/shop", want: "/v1/orgs/acme/projects/shop"},
		{name: "org path untouched", org: "acme", method: "POST", path: "/v1/orgs/acme/members", want: "/v1/orgs/acme/members"},
		{name: "whoami read", org: "acme", method: "GET", path: "/v1/account", want: "/v1/account"},
		{name: "account write refused", org: "acme", method: "PATCH", path: "/v1/account", refused: true},
		{name: "personal key refused", org: "acme", method: "POST", path: "/v1/keys", refused: true},
		{name: "billing cancel refused", org: "acme", method: "POST", path: "/v1/billing/cancel", refused: true},
		{name: "budget channels write refused", org: "acme", method: "PUT", path: "/v1/billing/budgets/b1/channels", refused: true},
		{name: "budget channels read refused", org: "acme", method: "GET", path: "/v1/billing/budgets/b1/channels", refused: true},
		{name: "budget channel item refused", org: "acme", method: "DELETE", path: "/v1/billing/budgets/b1/channels/c1", refused: true},
		{name: "account delete refused", org: "acme", method: "DELETE", path: "/v1/account?include_managed_postgres_databases=true", refused: true},
		{name: "account export refused", org: "acme", method: "GET", path: "/v1/account/export", refused: true},
		{name: "usage", org: "acme", method: "GET", path: "/v1/usage?month=2026-11", want: "/v1/orgs/acme/usage?month=2026-11"},
		{name: "daily usage refused", org: "acme", method: "GET", path: "/v1/usage/daily?days=7", refused: true},
		{name: "forecast refused", org: "acme", method: "GET", path: "/v1/billing/forecast", refused: true},
		{name: "personal key list refused", org: "acme", method: "GET", path: "/v1/keys", refused: true},
		{name: "personal storage usage refused", org: "acme", method: "GET", path: "/v1/account/object-storage-usage", refused: true},
		{name: "postgres overage refused", org: "acme", method: "GET", path: "/v1/postgres/overage", refused: true},
		{name: "account read kept", org: "acme", method: "GET", path: "/v1/account/rate-limits", want: "/v1/account/rate-limits"},
		{name: "postgres flat-only kept", org: "acme", method: "POST", path: "/v1/postgres/databases/db1/query", want: "/v1/postgres/databases/db1/query"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClient("https://api.example", "").WithProjectOrganization(tt.org)
			got, err := c.orgRoute(tt.method, tt.path)
			if tt.refused {
				var p *Problem
				if !errors.As(err, &p) || p.Code != CodeUnsupportedByCLI {
					t.Fatalf("orgRoute(%s %q) = %q, %v; want unsupported_by_cli", tt.method, tt.path, got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("orgRoute(%s %q) = %q, %v; want %q", tt.method, tt.path, got, err, tt.want)
			}
		})
	}
}

func TestProjectRoute(t *testing.T) {
	for _, tt := range []struct {
		name, org, path, want string
	}{
		{name: "personal", path: "/v1/projects/shop", want: "/v1/projects/shop"},
		{name: "list", org: "acme", path: "/v1/projects", want: "/v1/orgs/acme/projects"},
		{name: "nested with query", org: "acme", path: "/v1/projects/shop/environments?x=1", want: "/v1/orgs/acme/projects/shop/environments?x=1"},
		{name: "escaped org", org: "a b", path: "/v1/projects/shop", want: "/v1/orgs/a%20b/projects/shop"},
		{name: "non-project path", org: "acme", path: "/v1/apps/demo", want: "/v1/apps/demo"},
		{name: "lookalike prefix", org: "acme", path: "/v1/projectsx", want: "/v1/projectsx"},
		{name: "apply-plan", org: "acme", path: "/v1/projects/apply-plan", want: "/v1/orgs/acme/projects/apply-plan"},
		{name: "safe-deploy", org: "acme", path: "/v1/projects/safe-deploy", want: "/v1/orgs/acme/projects/safe-deploy"},
		{name: "source-ref", org: "acme", path: "/v1/projects/scan/source-ref", want: "/v1/orgs/acme/projects/scan/source-ref"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClient("https://api.example", "").WithProjectOrganization(tt.org)
			got, err := c.projectRoute(tt.path)
			if err != nil || got != tt.want {
				t.Fatalf("projectRoute(%q) = %q, %v; want %q", tt.path, got, err, tt.want)
			}
		})
	}
}

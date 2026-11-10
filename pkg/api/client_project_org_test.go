package api

import (
	"errors"
	"testing"
)

func TestProjectRoute(t *testing.T) {
	for _, tt := range []struct {
		name, org, path, want string
		refused               bool
	}{
		{name: "personal", path: "/v1/projects/shop", want: "/v1/projects/shop"},
		{name: "list", org: "acme", path: "/v1/projects", want: "/v1/orgs/acme/projects"},
		{name: "nested with query", org: "acme", path: "/v1/projects/shop/environments?x=1", want: "/v1/orgs/acme/projects/shop/environments?x=1"},
		{name: "escaped org", org: "a b", path: "/v1/projects/shop", want: "/v1/orgs/a%20b/projects/shop"},
		{name: "non-project path", org: "acme", path: "/v1/apps/demo", want: "/v1/apps/demo"},
		{name: "lookalike prefix", org: "acme", path: "/v1/projectsx", want: "/v1/projectsx"},
		{name: "apply-plan personal only", org: "acme", path: "/v1/projects/apply-plan", refused: true},
		{name: "source-ref personal only", org: "acme", path: "/v1/projects/scan/source-ref", refused: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClient("https://api.example", "").WithProjectOrganization(tt.org)
			got, err := c.projectRoute(tt.path)
			if tt.refused {
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.Problem.Code != CodeValidation {
					t.Fatalf("projectRoute(%q) = %q, %v; want validation problem", tt.path, got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("projectRoute(%q) = %q, %v; want %q", tt.path, got, err, tt.want)
			}
		})
	}
}

package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261126175800 — republishing after a tool change keeps the
// published title, summary, registry name and usage badge unless their
// flags are given.
func TestMCPListingKeep(t *testing.T) {
	current := api.MCPListingResponse{Title: "Web search", Summary: "Search the web.", RegistryName: "io.github.acme/search", ShowUsage: true}
	cases := []struct {
		name string
		req  api.PutMCPListingRequest
		set  map[string]bool
		want api.PutMCPListingRequest
	}{
		{name: "no flags keeps everything", req: api.PutMCPListingRequest{}, set: map[string]bool{},
			want: api.PutMCPListingRequest{Title: "Web search", Summary: "Search the web.", RegistryName: "io.github.acme/search", ShowUsage: true}},
		{name: "given flags win", req: api.PutMCPListingRequest{Title: "Search", Summary: ""},
			set:  map[string]bool{"title": true, "summary": true, "show-usage": true},
			want: api.PutMCPListingRequest{Title: "Search", RegistryName: "io.github.acme/search"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := main.MCPListingKeep(tc.req, current, tc.set)
			if got.Title != tc.want.Title || got.Summary != tc.want.Summary || got.RegistryName != tc.want.RegistryName || got.ShowUsage != tc.want.ShowUsage {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

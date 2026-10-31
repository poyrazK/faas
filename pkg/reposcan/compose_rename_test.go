package reposcan

import (
	"strings"
	"testing"
	"testing/fstest"
)

// ADR-20261029090000: x-gregale-renamed-from carries a service's previous
// name to the workload, and a hint that cannot name a previous app fails the
// scan with the reason.
func TestComposeRenamedFrom(t *testing.T) {
	scan := func(compose string) (Result, error) {
		return Scan(fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte(compose)}})
	}
	result, err := scan("services:\n  accounts:\n    image: ghcr.io/example/accounts:1\n    x-gregale-renamed-from: ledger\n  web:\n    image: ghcr.io/example/web:1\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, workload := range result.Workloads {
		want := map[string]string{"accounts": "ledger", "web": ""}[workload.Name]
		if workload.RenamedFrom != want {
			t.Fatalf("%s RenamedFrom = %q, want %q", workload.Name, workload.RenamedFrom, want)
		}
	}

	for _, tc := range []struct{ name, compose, want string }{
		{"itself", "services:\n  accounts:\n    image: ghcr.io/example/accounts:1\n    x-gregale-renamed-from: accounts\n",
			"names the service itself"},
		{"still a service", "services:\n  accounts:\n    image: ghcr.io/example/accounts:1\n    x-gregale-renamed-from: ledger\n  ledger:\n    image: ghcr.io/example/ledger:1\n",
			`names "ledger", which is still a service in this file`},
		{"claimed twice", "services:\n  accounts:\n    image: ghcr.io/example/accounts:1\n    x-gregale-renamed-from: ledger\n  billing:\n    image: ghcr.io/example/billing:1\n    x-gregale-renamed-from: ledger\n",
			`names "ledger", which accounts also renames`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := scan(tc.compose); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("scan error = %v, want %q", err, tc.want)
			}
		})
	}
}

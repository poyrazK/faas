// adr: 20261124090000
package reposcan

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestServiceAccessFromComposeAndProjectConfig(t *testing.T) {
	fsys := fstest.MapFS{
		"compose.yaml": {Data: []byte(`services:
  vote:
    image: ghcr.io/x/vote:1
    ports: ["80:80"]
    x-gregale-access: Public
  result: {image: ghcr.io/x/result:1, ports: ["81:80"]}
  api: {image: ghcr.io/x/api:1, ports: ["82:80"]}
`)},
		"gregale.yaml": {Data: []byte("project:\n  services:\n    result:\n      access: public\n    api:\n      access: key\n")},
	}
	result, err := Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, w := range result.Workloads {
		got[w.Name] = w.Access
		if PlanWorkload(w).Access != w.Access {
			t.Errorf("%s plan access = %q, want %q", w.Name, PlanWorkload(w).Access, w.Access)
		}
	}
	if got["vote"] != "public" || got["result"] != "public" || got["api"] != "key" {
		t.Fatalf("access = %v", got)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "x-gregale-access") {
			t.Fatalf("key reported as unsupported: %s", warning)
		}
	}
}

func TestServiceAccessRejectsUnknownValues(t *testing.T) {
	fsys := fstest.MapFS{"compose.yaml": {Data: []byte("services:\n  vote:\n    image: ghcr.io/x/vote:1\n    ports: [\"80:80\"]\n    x-gregale-access: open\n")}}
	if _, err := Scan(fsys); err == nil || !strings.Contains(err.Error(), "x-gregale-access") {
		t.Fatalf("err = %v", err)
	}
	config := fstest.MapFS{
		"compose.yaml": {Data: []byte("services:\n  vote: {image: ghcr.io/x/vote:1, ports: [\"80:80\"]}\n")},
		"gregale.yaml": {Data: []byte("project:\n  services:\n    vote:\n      access: anyone\n")},
	}
	if _, err := Scan(config); err == nil || !strings.Contains(err.Error(), "access must be public or key") {
		t.Fatalf("err = %v", err)
	}
}

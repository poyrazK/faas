// adr: 20261112223000

package reposcan

import (
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
)

// The voting app (dockersamples/example-voting-app) dials redis and db by
// their Compose names from three languages; the 2026-11-13 eval's agent found
// each one by reading the code.
func TestDetectSourceHostsFindsComposeNames(t *testing.T) {
	fsys := fstest.MapFS{
		"vote/app.py":          {Data: []byte("import os\nfrom redis import Redis\n\ndef get_redis():\n    g.redis = Redis(host=\"redis\", db=0, socket_timeout=5)\n")},
		"result/server.js":     {Data: []byte("var pool = new Pool({\n  connectionString: 'postgres://postgres:postgres@db/postgres'\n});\n")},
		"worker/Program.cs":    {Data: []byte("var pgsql = OpenDbConnection(\"Server=db;Username=postgres;Password=postgres;\");\nvar redisConn = OpenRedisConnection(\"redis\");\n")},
		"worker/env.cs":        {Data: []byte("var host = Environment.GetEnvironmentVariable(\"REDIS\") ?? \"redis\";\n")},
		"vote/tests/t_test.py": {Data: []byte("Redis(host=\"redis\")\n")},
		"vote/notes.py":        {Data: []byte("# Redis(host=\"redis\") in a comment\nprint('redis is fast')\n")},
	}
	workloads := []Workload{{Name: "vote", RootDir: "vote"}, {Name: "result", RootDir: "result"}, {Name: "worker", RootDir: "worker"},
		{Name: "redis", Image: "redis:alpine"}}
	managed := []Managed{{Name: "db", Kind: "postgres"}}
	detectSourceHosts(fsys, workloads, managed)

	want := map[string][]api.PlanSourceHost{
		"vote":   {{Host: "redis", File: "vote/app.py", Line: 5}},
		"result": {{Host: "db", File: "result/server.js", Line: 2}},
		"worker": {{Host: "db", File: "worker/Program.cs", Line: 1}, {Host: "redis", File: "worker/Program.cs", Line: 2}},
	}
	for _, w := range workloads {
		got := w.SourceHosts
		if len(got) != len(want[w.Name]) {
			t.Errorf("%s: source hosts = %+v, want %+v", w.Name, got, want[w.Name])
			continue
		}
		for i := range got {
			if got[i] != want[w.Name][i] {
				t.Errorf("%s[%d] = %+v, want %+v", w.Name, i, got[i], want[w.Name][i])
			}
		}
	}
}

// A service never matches its own name, and words that only mention a
// service are not hosts.
func TestDetectSourceHostsIgnoresSelfAndProse(t *testing.T) {
	fsys := fstest.MapFS{
		"api/main.go": {Data: []byte("package main\n// api calls db\nconst help = \"use the db command\"\nvar self = \"http://api:8080/\"\n")},
	}
	workloads := []Workload{{Name: "api", RootDir: "api"}, {Name: "db", Image: "postgres"}}
	detectSourceHosts(fsys, workloads, nil)
	if len(workloads[0].SourceHosts) != 0 {
		t.Fatalf("source hosts = %+v, want none", workloads[0].SourceHosts)
	}
}

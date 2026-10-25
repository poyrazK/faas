package gregalemanifest

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// apid parses customer manifests server-side (source-ref deploys, inline
// trigger batches). Whatever the bytes, parsing and the derived views apid
// reads must return an error, never panic.
func FuzzParseManifest(f *testing.F) {
	for _, seed := range []string{
		"async_routes:\n  - app: reports\n    name: create-report\n    match_host: reports.example.com\n    match_path: /reports\n    retry_policy:\n      max_attempts: 4\n      base_seconds: 1\n      max_seconds: 30\n    max_age_seconds: 600\n",
		"worker:\n  command: ./consumer\n  drain_timeout: 45s\n  scale:\n    min: 0\n    max: 50\n    metric: queue_lag\n    target: 500\n  source:\n    kind: kafka\n    config:\n      brokers: [\"localhost:9092\"]\n      topic: events\n",
		"main_depends_on:\n  - name: db\n",
		"companions:\n  - name: proxy\n    image: nginx\n    port: 8080\n",
		"triggers:\n  - kind: cron\n    schedule: \"*/5 * * * *\"\n",
		"[[main_depends_on]]\nname = \"db\"\n",
		"a: &x [*x, *x]\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, parse := range []func([]byte) (*Manifest, error){ParseBytes, ParseTOMLBytes} {
			m, err := parse(data)
			if err != nil || m == nil {
				continue
			}
			_ = m.Validate()
			_ = m.ValidateForPlan(api.PlanScale)
			_, _ = m.ToSidecars()
			_ = m.MainWorkloadDependencies()
			_ = m.TemplatedPathWarnings()
		}
	})
}

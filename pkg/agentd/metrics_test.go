// adr: 20261027090010
package agentd_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/agentd"
)

// nameRecorder is a Registerer that keeps the names of what registers on it.
type nameRecorder struct{ names map[string]bool }

var descName = regexp.MustCompile(`fqName: "([^"]+)"`)

func (r *nameRecorder) Register(c prometheus.Collector) error {
	descs := make(chan *prometheus.Desc)
	go func() { c.Describe(descs); close(descs) }()
	for d := range descs {
		if m := descName.FindStringSubmatch(d.String()); m != nil {
			r.names[m[1]] = true
		}
	}
	return nil
}

func (r *nameRecorder) MustRegister(cs ...prometheus.Collector) {
	for _, c := range cs {
		_ = r.Register(c)
	}
}

func (r *nameRecorder) Unregister(prometheus.Collector) bool { return true }

// The agent runs dashboard queries only series agentd registers: dashboards
// depend on exact names.
func TestDashboardNamesAgentdMetrics(t *testing.T) {
	reg := &nameRecorder{names: map[string]bool{}}
	if _, err := agentd.NewMetrics(reg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../deploy/grafana/agent-runs.json")
	if err != nil {
		t.Fatal(err)
	}
	queried := regexp.MustCompile(`\bagent_[a-z_]+`).FindAllString(string(raw), -1)
	if len(queried) == 0 {
		t.Fatal("the dashboard queries no agent series")
	}
	for _, name := range queried {
		family := name
		for _, suffix := range []string{"_bucket", "_count", "_sum"} {
			family = strings.TrimSuffix(family, suffix)
		}
		if !reg.names[family] {
			t.Errorf("the dashboard queries %s, which agentd does not register", name)
		}
	}
	for name := range reg.names {
		if !strings.Contains(string(raw), name) {
			t.Errorf("agentd registers %s, which the dashboard does not show", name)
		}
	}
}

// adr: 1344 — promoting the warm standby (§8).
package daemonunitspec

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A planned switchover must stop every writer and fence the old primary
// before it reads the shutdown checkpoint the standby has to replay; a
// failover must name the primary the operator fenced.
func TestPostgresPromotePlaybook(t *testing.T) {
	plays := loadYAMLList(t, filepath.Join(repoRoot(t), "deploy", "ansible", "postgres_promote.yml"))
	if len(plays) != 3 {
		t.Fatalf("postgres_promote.yml has %d plays, want check, fence, promote", len(plays))
	}
	hosts := []string{}
	taskNames := make([][]string, len(plays))
	tasks := make([]map[string]map[string]any, len(plays))
	for i, item := range plays {
		play := item.(map[string]any)
		hosts = append(hosts, play["hosts"].(string))
		tasks[i] = map[string]map[string]any{}
		for _, raw := range play["tasks"].([]any) {
			task := raw.(map[string]any)
			name := task["name"].(string)
			taskNames[i] = append(taskNames[i], name)
			tasks[i][name] = task
		}
	}
	if strings.Join(hosts, ",") != "postgres_standby,control_plane,postgres_standby" {
		t.Fatalf("play hosts = %v", hosts)
	}

	fenced, _ := tasks[0]["Require the fenced primary to be named for a failover"]["ansible.builtin.assert"].(map[string]any)
	if that := fenced["that"].([]any)[0].(string); !strings.Contains(that, "faas_promote_fenced_primary") {
		t.Errorf("failover does not require the fenced primary's name: %s", that)
	}

	order := []string{
		"Stop every faas unit on the primary",
		"Stop PostgreSQL on the primary",
		"Mask PostgreSQL on the old primary",
		"Read the primary's shutdown checkpoint",
		"Require a clean shutdown",
	}
	last := -1
	for _, want := range order {
		at := slices.Index(taskNames[1], want)
		if at <= last {
			t.Fatalf("fence play must run %v in order; got %v", order, taskNames[1])
		}
		last = at
	}
	if skip := tasks[1]["Skip unless this is a planned switchover"]; skip["when"] != "faas_promote_mode != 'planned'" {
		t.Error("the fence play runs during a failover, when the primary may be unreachable")
	}

	wait := tasks[2]["Wait until the standby has replayed the primary's shutdown checkpoint"]
	if wait["when"] != "faas_promote_mode == 'planned'" {
		t.Error("the replay wait is not limited to planned switchovers")
	}
	waitArgs, _ := wait["ansible.builtin.command"].(map[string]any)
	if argv := fmt.Sprint(waitArgs["argv"]); !strings.Contains(argv, "pg_last_wal_replay_lsn() >") || !strings.Contains(argv, "promote_primary_checkpoint_lsn") {
		t.Errorf("replay wait does not compare replay to the shutdown checkpoint: %s", argv)
	}
	if at, promote := slices.Index(taskNames[2], "Wait until the standby has replayed the primary's shutdown checkpoint"), slices.Index(taskNames[2], "Promote the standby to primary"); at < 0 || promote < at {
		t.Error("the standby is promoted before it replays the shutdown checkpoint")
	}
}

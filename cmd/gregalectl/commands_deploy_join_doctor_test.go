package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/releaseinstall"
	"gopkg.in/yaml.v3"
)

type joinDoctorScopeStore struct {
	releaseinstall.Store
	nodes []releaseinstall.ComputeNodeRow
}

func (s *joinDoctorScopeStore) ListComputeNodes(context.Context) ([]releaseinstall.ComputeNodeRow, error) {
	return s.nodes, nil
}

// Derive the scope from the actual adoption command, so this reproduces the
// shared-history/local-disk gate rather than merely testing the CLI option.
func nodeJoinDoctorReleaseScope(t *testing.T, candidate string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var plays []struct {
		Tasks []struct {
			Name    string    `yaml:"name"`
			Command yaml.Node `yaml:"ansible.builtin.command"`
		} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(body, &plays); err != nil {
		t.Fatal(err)
	}
	for _, play := range plays {
		for _, task := range play.Tasks {
			if task.Name != "Run the node-scoped doctor before starting services" {
				continue
			}
			var command struct {
				Argv []string `yaml:"argv"`
			}
			if err := task.Command.Decode(&command); err != nil {
				t.Fatal(err)
			}
			for i, arg := range command.Argv {
				if arg == "--release" && i+1 < len(command.Argv) {
					return strings.ReplaceAll(command.Argv[i+1], "{{ faas_join_release_git_sha }}", candidate)
				}
			}
			return ""
		}
	}
	t.Fatal("node adoption doctor command missing")
	return ""
}

func TestNodeJoinDoctorCandidateScopePreservesHistoricalDiagnostics(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	candidate := strings.Repeat("a", 40)
	historical := strings.Repeat("b", 40)
	if err := os.Mkdir(filepath.Join(root, candidate), 0o755); err != nil {
		t.Fatal(err)
	}
	deps := &doctorDeps{
		releasesRoot:  root,
		releaseFilter: nodeJoinDoctorReleaseScope(t, candidate),
		store:         &joinDoctorScopeStore{},
		bundlesBySHA: map[string]releaseinstall.BundleRow{
			candidate: {GitSHA: candidate}, historical: {GitSHA: historical},
		},
	}
	findings, err := checkBundleOrphans(ctx, deps)
	if err != nil || len(findings) != 1 || findings[0].Severity != doctorSeverityOK {
		t.Fatalf("candidate adoption blocked by peer history: findings=%+v err=%v", findings, err)
	}
	deps.releaseFilter = ""
	findings, err = checkBundleOrphans(ctx, deps)
	if err != nil || len(findings) != 1 || findings[0].Severity != doctorSeverityWarn || findings[0].Target != historical {
		t.Fatalf("unfiltered historical warning lost: findings=%+v err=%v", findings, err)
	}
	deps.releaseFilter = nodeJoinDoctorReleaseScope(t, candidate)
	if err := os.Remove(filepath.Join(root, candidate)); err != nil {
		t.Fatal(err)
	}
	findings, err = checkBundleOrphans(ctx, deps)
	if err != nil || len(findings) != 1 || findings[0].Severity != doctorSeverityWarn || findings[0].Target != candidate {
		t.Fatalf("missing candidate accepted: findings=%+v err=%v", findings, err)
	}
}

func TestNodeJoinDoctorScopeStillRejectsInvalidNodeMembership(t *testing.T) {
	candidate := strings.Repeat("a", 40)
	for _, releaseID := range []string{"", "invalid", strings.Repeat("b", 40)} {
		t.Run("release="+releaseID, func(t *testing.T) {
			deps := &doctorDeps{
				nodeFilter:    "compute-1",
				releaseFilter: nodeJoinDoctorReleaseScope(t, candidate),
				store:         &joinDoctorScopeStore{nodes: []releaseinstall.ComputeNodeRow{{Name: "compute-1", ReleaseID: releaseID}}},
				bundlesBySHA:  map[string]releaseinstall.BundleRow{candidate: {GitSHA: candidate}},
			}
			findings, err := checkNodes(t.Context(), deps)
			if err != nil || len(findings) != 1 || findings[0].Severity != doctorSeverityError {
				t.Fatalf("invalid membership hidden by candidate scope: findings=%+v err=%v", findings, err)
			}
		})
	}
}

// adr: 20261027090013 step 10 part three — a template is unique per account,
// its commands change in place, and it reports its newest build and the
// newest finished one, which is what sessions boot.
package agents_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

func TestSandboxTemplates(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	var app string
	if err := pool.QueryRow(ctx, `INSERT INTO apps (account_id, slug, sandbox_template) VALUES ($1, 'sbt-0123456789ab', true) RETURNING id::text`,
		account).Scan(&app); err != nil {
		t.Fatal(err)
	}
	spec := agents.SandboxTemplateSpec{StartCmd: "serve", ReadyCmd: "probe", ReadyTimeoutSec: 30}
	if _, err := s.CreateSandboxTemplate(ctx, account, "tools", app, spec); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSandboxTemplate(ctx, account, "tools", app, spec); !errors.Is(err, agents.ErrTemplateExists) {
		t.Fatalf("a second create = %v", err)
	}
	got, err := s.SandboxTemplate(ctx, account, "tools")
	if err != nil || got.AppSlug != "sbt-0123456789ab" || got.SandboxTemplateSpec != spec || got.Ready() || got.BuildID != "" {
		t.Fatalf("template = %+v, %v", got, err)
	}
	if _, err := s.SandboxTemplate(ctx, other, "tools"); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("another account read it: %v", err)
	}

	deploy := func(status, key string, at time.Time) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO deployments (app_id, status, rootfs_key, created_at, error) VALUES ($1, $2, $3, $4, nullif($5, ''))
			RETURNING id::text`, app, status, key, at, map[bool]string{true: "build failed"}[status == "failed"]).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	now := time.Now()
	live := deploy("live", "apps/sbt-0123456789ab/a.ext4", now.Add(-time.Hour))
	failed := deploy("failed", "", now)
	got, _ = s.SandboxTemplate(ctx, account, "tools")
	if !got.Ready() || got.LiveDeploymentID != live || got.LiveRootfsKey != "apps/sbt-0123456789ab/a.ext4" ||
		got.BuildID != failed || got.BuildStatus != "failed" || got.BuildError != "build failed" {
		t.Fatalf("built template = %+v", got)
	}

	spec.StartCmd = ""
	if err := s.UpdateSandboxTemplateSpec(ctx, account, "tools", spec); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSandboxTemplateSpec(ctx, account, "nope", spec); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("update of a missing template = %v", err)
	}
	if list, err := s.SandboxTemplates(ctx, account); err != nil || len(list) != 1 || list[0].StartCmd != "" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if appID, err := s.DeleteSandboxTemplate(ctx, account, "tools"); err != nil || appID != app {
		t.Fatalf("delete = %q, %v", appID, err)
	}
	if _, err := s.DeleteSandboxTemplate(ctx, account, "tools"); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("a second delete = %v", err)
	}
}

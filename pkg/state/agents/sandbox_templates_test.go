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

// A session from a template boots the template's newest finished build, and
// fails as image_unavailable when the template is deleted before it starts.
func TestSandboxFromATemplate(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	template := func(name, slug string, keys ...string) string {
		t.Helper()
		var app string
		if err := pool.QueryRow(ctx, `INSERT INTO apps (account_id, slug) VALUES ($1, $2) RETURNING id::text`, account, slug).Scan(&app); err != nil {
			t.Fatal(err)
		}
		for i, key := range keys {
			if _, err := pool.Exec(ctx, `INSERT INTO deployments (app_id, status, rootfs_key, created_at) VALUES ($1, 'live', $2, $3)`,
				app, key, time.Now().Add(time.Duration(i-len(keys))*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		id, err := s.CreateSandboxTemplate(ctx, account, name, app, agents.SandboxTemplateSpec{StartCmd: "serve", ReadyCmd: "probe", ReadyTimeoutSec: 60})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	tools := template("tools", "sbt-0123456789ab", "apps/sbt-0123456789ab/old.ext4", "apps/sbt-0123456789ab/new.ext4")
	gone := template("gone", "sbt-ba9876543210", "apps/sbt-ba9876543210/a.ext4")
	sb := admitSandbox(t, s, agents.SandboxAdmission{Preset: "template", ImageRef: "template:tools", TemplateID: tools})
	doomed := admitSandbox(t, s, agents.SandboxAdmission{Preset: "template", ImageRef: "template:gone", TemplateID: gone})
	if _, err := s.DeleteSandboxTemplate(ctx, account, "gone"); err != nil {
		t.Fatal(err)
	}
	if _, failed, _, err := s.ExpireSandboxLeases(ctx); err != nil || failed != 1 {
		t.Fatalf("expire = %d failed, %v", failed, err)
	}
	got, _ := s.Sandbox(ctx, account, doomed.ID)
	if got.State != agents.SandboxFailed || got.OutcomeCode != "image_unavailable" || got.OutcomeMessage != "the template gone was deleted before the session started" {
		t.Fatalf("session of a deleted template = %+v", got)
	}
	if got, _ := s.Sandbox(ctx, account, sb.ID); got.State != agents.SandboxQueued || got.ImageRef != "template:tools" {
		t.Fatalf("template session = %+v", got)
	}
	claim, found, err := s.ClaimSandbox(ctx, "agentd", "tok", ttl, maxLive(5))
	if err != nil || !found || claim.ID != sb.ID || claim.ImageKey != "apps/sbt-0123456789ab/new.ext4" || claim.ImageMissing ||
		claim.StartCmd != "serve" || claim.ReadyCmd != "probe" || claim.ReadyTimeout != time.Minute {
		t.Fatalf("claim = %+v, %v, %v", claim, found, err)
	}
}

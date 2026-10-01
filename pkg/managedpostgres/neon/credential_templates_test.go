// adr: 388 — release-only managed migration credential delivery.

package neon

import (
	"context"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Run the shipped JavaScript against the same restricted roles that the
// provider issues. Dependencies live outside the embedded template tree.
func TestPostgresStartersUseMigrationRolesAndSerializeReleases(t *testing.T) {
	root := os.Getenv("GREGALE_POSTGRES_STARTERS_DIR")
	if root == "" {
		t.Skip("set GREGALE_POSTGRES_STARTERS_DIR to installed disposable starter copies")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	f := newCredentialFixture(t)
	ctx := context.Background()
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	// Issue runtime first, so migrations exercise future-object default ACLs.
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	dsn := url.URL{Scheme: "postgresql", User: url.User(f.migration.name), Host: net.JoinHostPort(f.config.Host, strconv.Itoa(int(f.config.Port))), Path: "/" + f.config.Database, RawQuery: "sslmode=disable"}
	for _, tc := range []struct {
		folder, entry string
		lock          int64
	}{
		{"rest-api-postgres", "migrate.js", 734928146},
		{"customer-platform", "app/migrate.js", 734928145},
	} {
		t.Run(tc.folder, func(t *testing.T) {
			// Both children must wait on the database lock, proving that an
			// overlapping deployment cannot execute its schema concurrently.
			if _, err := f.admin.Exec(ctx, "BEGIN"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.admin.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", tc.lock); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = f.admin.Exec(ctx, "ROLLBACK") })
			childCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			commands := make([]*exec.Cmd, 2)
			for i := range commands {
				commands[i] = exec.CommandContext(childCtx, node, filepath.Join(root, tc.folder, tc.entry))
				commands[i].Env = append(os.Environ(), "MIGRATION_DATABASE_URL="+dsn.String(), "DATABASE_URL=unused-runtime-credential")
				if err := commands[i].Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if commands[i].ProcessState == nil {
						_ = commands[i].Process.Kill()
						_ = commands[i].Wait()
					}
				})
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := f.admin.Exec(ctx, "SELECT pg_stat_clear_snapshot()"); err != nil {
					t.Fatal(err)
				}
				var waiting int
				err := f.admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND datname=$2 AND wait_event='advisory'`, f.migration.name, f.config.Database).Scan(&waiting)
				if err != nil {
					t.Fatal(err)
				}
				if waiting == 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("%d migration processes waiting, want 2", waiting)
				}
				time.Sleep(20 * time.Millisecond)
			}
			if _, err := f.admin.Exec(ctx, "COMMIT"); err != nil {
				t.Fatal(err)
			}
			for _, command := range commands {
				if err := command.Wait(); err != nil {
					t.Fatalf("starter migration failed: %v", err)
				}
			}
		})
	}
	runtime := f.connect(t, f.runtime.name)
	executeSQL(t, runtime, `INSERT INTO public.notes (body) VALUES ('serving works'); UPDATE public.notes SET body='updated'`)
	deniedSQL(t, runtime, `CREATE TABLE public.runtime_ddl (id integer)`)
	deniedSQL(t, runtime, `ALTER TABLE public.notes ADD COLUMN forbidden text`)
	executeSQL(t, runtime, `SELECT set_config('gregale.platform_tenant_id','11111111-1111-1111-1111-111111111111',false)`)
	executeSQL(t, runtime, `INSERT INTO public.customer_documents (tenant_id,id,title,content) VALUES ('11111111-1111-1111-1111-111111111111','22222222-2222-2222-2222-222222222222','isolated','data')`)
	// Login rotation cannot take ownership away from either starter's tables.
	if err := f.manager.Revoke(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM public.notes`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("runtime after migration retirement: %d, %v", count, err)
	}
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM public.customer_documents`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("tenant data after retirement: %d, %v", count, err)
	}
}

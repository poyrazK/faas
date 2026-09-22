package e2etest

// Boot-order contracts for Harness.Start. Both are start-order races that made
// every wake and every image deploy in the smoke lane fail (run 35157946150):
// schedd's first heartbeat dialing a vmmd socket that did not exist yet, and
// tests POSTing deployments before imaged had subscribed.

import (
	"context"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestStart_BootsVMMDBeforeSchedd(t *testing.T) {
	src, err := os.ReadFile("harness.go")
	if err != nil {
		t.Fatal(err)
	}
	start := regexp.MustCompile(`(?s)func Start\(.*?\n}\n`).Find(src)
	if start == nil {
		t.Fatal("cannot locate func Start in harness.go")
	}
	vmmd := regexp.MustCompile(`if which&VMMD != 0 \{`).FindIndex(start)
	schedd := regexp.MustCompile(`if which&Schedd != 0 \{`).FindIndex(start)
	if vmmd == nil || schedd == nil {
		t.Fatalf("Start does not boot both vmmd (%v) and schedd (%v)", vmmd, schedd)
	}
	if vmmd[0] > schedd[0] {
		t.Error("Start boots schedd before vmmd: schedd's first heartbeat dials the vmmd socket " +
			"immediately, one ENOENT marks default-local unavailable for 30s, and every wake " +
			"fails with \"no active compute_node fits ... across 0 candidates\"")
	}
}

func TestStart_WaitsForImagedToSubscribe(t *testing.T) {
	src, err := os.ReadFile("harness.go")
	if err != nil {
		t.Fatal(err)
	}
	// The wait lives in startImaged now, which Start and StartWithEnv share.
	// Asserting on the helper rather than on Start's if-block covers BOTH
	// entry points: StartWithEnv used to ignore the Imaged bit entirely, so a
	// test could ask for imaged, get none, and see its deployment sit in
	// `pending` with nothing reported.
	helper := regexp.MustCompile(`(?s)func startImaged\(.*?\n}\n`).Find(src)
	if helper == nil {
		t.Fatal("cannot locate func startImaged in harness.go")
	}
	if !regexp.MustCompile(`waitImagedListens\(`).Match(helper) {
		t.Error("startImaged returns as soon as imaged's process is up; imaged stages bases for ~70s " +
			"before subscribing, and a deployment POSTed before that is lost until the 2h stale sweep")
	}
	for _, fn := range []string{`func Start\(`, `func StartWithEnv\(`} {
		body := regexp.MustCompile(`(?s)` + fn + `.*?\n}\n`).Find(src)
		if body == nil {
			t.Fatalf("cannot locate %s in harness.go", fn)
		}
		if !regexp.MustCompile(`startImaged\(`).Match(body) {
			t.Errorf("%s does not delegate to startImaged; a caller asking for imaged would silently get none", fn)
		}
	}
}

// The probe must see a LISTEN, not merely a session: imaged holds pool
// sessions long before it subscribes.
func TestSessionListens_RequiresALISTEN(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()

	cfg := pool.Config()
	cfg.ConnConfig.RuntimeParams["application_name"] = "e2etest-probe"
	named, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer named.Close()
	if err := named.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	// A live session that has NOT listened must not count.
	if ok, err := sessionListens(ctx, pool, "e2etest-probe"); err != nil || ok {
		t.Fatalf("session without LISTEN reported ok=%v err=%v; a still-staging imaged would be "+
			"mistaken for a subscribed one", ok, err)
	}

	conn, err := named.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `LISTEN e2etest_probe_channel`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ok, err := sessionListens(ctx, pool, "e2etest-probe")
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a session that issued LISTEN was never detected")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

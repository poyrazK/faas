package liveserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sdkEvent is a line the SDK driver prints.
type sdkEvent struct {
	Event  string   `json:"event"`
	Status string   `json:"status"`
	Rows   []string `json:"rows"`
	Error  *string  `json:"error"`
	Xid    string   `json:"xid"`
}

// adr: 20261016151947 — the JavaScript SDK holds a live query end to end against real
// PostgreSQL: the initial result, changes, awaitTxId, seal renewal, resuming
// after the connection drops, and the close when the table goes away. It
// needs node and a built SDK (npm run build in sdk/data).
func TestSDKEndToEnd(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	sdk, err := filepath.Abs("../../../sdk/data")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sdk, "dist/live/index.js")); err != nil {
		t.Skip("SDK not built: run npm run build in sdk/data")
	}
	f := newFixture(t, schema, Config{})
	feed := `SELECT id, body FROM messages WHERE channel_id = $1 ORDER BY id DESC LIMIT 3`
	var seals atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/seal", func(w http.ResponseWriter, _ *http.Request) {
		seals.Add(1)
		// Three-second seals make the SDK renew during the test.
		_, _ = fmt.Fprint(w, f.seal(f.secret, "db_1", feed, 3*time.Second, "1"))
	})
	sealSrv := httptest.NewServer(mux)
	t.Cleanup(sealSrv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join(sdk, "test/live-e2e.mjs"))
	cmd.Env = append(os.Environ(), "LIVE_URL=ws"+strings.TrimPrefix(f.srv.URL, "http")+"/live", "SEAL_URL="+sealSrv.URL+"/seal")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = testLog{t}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	events := make(chan sdkEvent, 256)
	go func() {
		defer close(events)
		scan := bufio.NewScanner(stdout)
		for scan.Scan() {
			var e sdkEvent
			if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
				t.Errorf("driver printed %q", scan.Text())
				continue
			}
			events <- e
		}
	}()
	expect := func(what string, ok func(sdkEvent) bool) sdkEvent {
		t.Helper()
		deadline := time.After(30 * time.Second)
		for {
			select {
			case e, open := <-events:
				if !open {
					t.Fatalf("driver exited waiting for %s", what)
				}
				if ok(e) {
					return e
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	command := func(v any) {
		data, _ := json.Marshal(v)
		if _, err := stdin.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	rows := func(e sdkEvent) string { return strings.Join(e.Rows, ",") }

	expect("the empty result", func(e sdkEvent) bool { return e.Status == "live" && len(e.Rows) == 0 })
	xid := f.write(`INSERT INTO messages VALUES (1, 1, 'a'), (2, 1, 'b'), (3, 2, 'other')`)
	command(map[string]any{"await": fmt.Sprint(xid)})
	if e := expect("awaitTxId", func(e sdkEvent) bool { return e.Event == "seen" }); rows(e) != "2:b,1:a" {
		t.Fatalf("after the first write: %s", rows(e))
	}

	// Past three seal lifetimes: the SDK renews in time and stays live.
	time.Sleep(10 * time.Second)
	xid = f.write(`INSERT INTO messages VALUES (4, 1, 'renewed')`)
	command(map[string]any{"await": fmt.Sprint(xid)})
	if e := expect("awaitTxId after renewals", func(e sdkEvent) bool { return e.Event == "seen" }); rows(e) != "4:renewed,2:b,1:a" {
		t.Fatalf("after renewals: %s", rows(e))
	}
	if n := seals.Load(); n < 3 {
		t.Fatalf("the SDK fetched %d seals in ten seconds of three-second seals", n)
	}

	// The connection drops; the SDK reconnects and resumes.
	f.dropConnections()
	xid = f.write(`INSERT INTO messages VALUES (5, 1, 'after reconnect')`)
	command(map[string]any{"await": fmt.Sprint(xid)})
	if e := expect("awaitTxId after reconnecting", func(e sdkEvent) bool { return e.Event == "seen" }); rows(e) != "5:after reconnect,4:renewed,2:b" {
		t.Fatalf("after reconnecting: %s", rows(e))
	}

	f.exec(`DROP TABLE messages`)
	e := expect("the close", func(e sdkEvent) bool { return e.Status == "closed" })
	if e.Error == nil || *e.Error != "schema_changed" {
		t.Fatalf("close: %+v", e)
	}
	command(map[string]any{"exit": true})
}

package main_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// `gregale logs <app> --follow --source http` was refused outright, so there
// was no way to watch requests land. It now polls the request rows and prints
// each row once, oldest first, including a row for an earlier minute that was
// flushed after a later one.
func TestCmdLogsFollowHTTPPrintsEachRowOnce(t *testing.T) {
	var polls atomic.Int32
	var sinces []string
	var mu sync.Mutex
	base := time.Now().UTC().Truncate(time.Minute)
	minute := func(offset int) string { return base.Add(time.Duration(offset) * time.Minute).Format(time.RFC3339) }
	row := func(id string, offset int) string {
		return fmt.Sprintf(`{"id":"row-%s","route":"GET /%s","method":"GET","status":500,"latency_ms":1,"count":1,"received_at":%q}`, id, id, minute(offset))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/myapp/debug/requests" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		sinces = append(sinces, r.URL.Query().Get("since"))
		mu.Unlock()
		if got := r.URL.Query().Get("status"); got != "500" {
			t.Errorf("status filter = %q, want 500", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch polls.Add(1) {
		case 1:
			_, _ = fmt.Fprintf(w, `{"complete":true,"requests":[%s,%s]}`, row("b", 0), row("a", -1))
		default:
			// Newest first, as the API answers. row-c is a late flush for
			// an earlier minute inside the backlog, row-d is new, and row-old
			// predates the 2m backlog the wider 5m poll window now reaches.
			_, _ = fmt.Fprintf(w, `{"complete":true,"requests":[%s,%s,%s,%s,%s]}`,
				row("d", 0), row("b", 0), row("a", -1), row("c", -1), row("old", -4))
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Cleanup(main.SetHTTPLogsFollowForTest(time.Millisecond, 3))

	var stdout, stderr bytes.Buffer
	oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
	(*main.OsStdout), (*main.OsStderr) = &stdout, &stderr
	t.Cleanup(func() { (*main.OsStdout), (*main.OsStderr) = oldOut, oldErr })

	if code := main.CmdLogs([]string{"myapp", "--follow", "--status", "500"}); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	var routes []string
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		_, rest, ok := strings.Cut(line, `route="`)
		if !ok {
			t.Fatalf("unexpected line %q", line)
		}
		route, _, _ := strings.Cut(rest, `"`)
		routes = append(routes, route)
	}
	if got, want := strings.Join(routes, ","), "GET /a,GET /b,GET /c,GET /d"; got != want {
		t.Fatalf("printed routes = %s, want %s\n%s", got, want, stdout.String())
	}
	if !strings.Contains(stderr.String(), "Following HTTP requests for myapp") || !strings.Contains(stderr.String(), "about a minute") {
		t.Errorf("stderr missing follow banner with lag note: %q", stderr.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sinces) != 3 || sinces[0] != "2m" || sinces[1] != "5m0s" {
		t.Errorf("since per poll = %v, want [2m 5m0s 5m0s]", sinces)
	}
}

func TestCmdLogsFollowHTTPRejectsLookupFlags(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	var stderr bytes.Buffer
	oldErr := (*main.OsStderr)
	(*main.OsStderr) = &stderr
	t.Cleanup(func() { (*main.OsStderr) = oldErr })

	for _, args := range [][]string{
		{"myapp", "--follow", "--source", "http", "--all"},
		{"myapp", "--follow", "--limit", "5"},
		{"myapp", "--follow", "--request", "req_1"},
	} {
		if code := main.CmdLogs(args); code != 2 {
			t.Errorf("%v exit = %d, want 2", args, code)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("rejected follow flags reached the API %d times", hits.Load())
	}
}

func TestCmdLogsEmptyHTTPResultExplainsLag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"complete":true,"requests":[]}`)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	var stderr bytes.Buffer
	oldErr := (*main.OsStderr)
	(*main.OsStderr) = &stderr
	t.Cleanup(func() { (*main.OsStderr) = oldErr })

	if code := main.CmdLogs([]string{"myapp", "--source", "http", "--since", "1m"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "No HTTP requests matched") || !strings.Contains(stderr.String(), "about a minute after") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// A runtime follow of an app that never prints (the hello-go starter) used to
// sit silent; after the idle delay it says once what runtime logs contain and
// how to watch requests instead.
func TestRuntimeFollowIdleHint(t *testing.T) {
	var stderr bytes.Buffer
	oldErr := (*main.OsStderr)
	(*main.OsStderr) = &stderr
	t.Cleanup(func() { (*main.OsStderr) = oldErr })
	t.Cleanup(main.SetRuntimeFollowIdleHintForTest(5 * time.Millisecond))

	markLine, stop := main.StartRuntimeFollowIdleHint("quiet-app")
	_ = markLine
	time.Sleep(50 * time.Millisecond)
	stop()
	if got := stderr.String(); !strings.Contains(got, "No runtime output yet") || !strings.Contains(got, "logs quiet-app --follow --source http") {
		t.Fatalf("hint = %q", got)
	}

	// Once per process: a reconnect does not repeat it.
	stderr.Reset()
	_, stop = main.StartRuntimeFollowIdleHint("quiet-app")
	time.Sleep(50 * time.Millisecond)
	stop()
	if stderr.Len() != 0 {
		t.Fatalf("hint repeated: %q", stderr.String())
	}
}

func TestRuntimeFollowIdleHintSilentAfterALine(t *testing.T) {
	var stderr bytes.Buffer
	oldErr := (*main.OsStderr)
	(*main.OsStderr) = &stderr
	t.Cleanup(func() { (*main.OsStderr) = oldErr })
	t.Cleanup(main.SetRuntimeFollowIdleHintForTest(20 * time.Millisecond))

	markLine, stop := main.StartRuntimeFollowIdleHint("chatty-app")
	markLine()
	time.Sleep(60 * time.Millisecond)
	stop()
	if stderr.Len() != 0 {
		t.Fatalf("hint printed after a runtime line: %q", stderr.String())
	}

	// The stream reconnecting after its idle timeout starts a new hint; an
	// app that already wrote lines must not be told it has no output.
	_, stop = main.StartRuntimeFollowIdleHint("chatty-app")
	time.Sleep(60 * time.Millisecond)
	stop()
	if stderr.Len() != 0 {
		t.Fatalf("hint printed after reconnect: %q", stderr.String())
	}
}

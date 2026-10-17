//go:build postfix

package internalmail

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestPostfixRelayBridge runs only in the disposable --network none fixture
// built by scripts/test-internal-mail-postfix.sh. It modifies that fixture's
// Postfix configuration and starts its real queue manager, smtp and pipe agents.
func TestPostfixRelayBridge(t *testing.T) {
	if os.Getenv("FAAS_TEST_POSTFIX") != "1" {
		t.Skip("run scripts/test-internal-mail-postfix.sh")
	}
	if _, err := os.Stat("/run/gregale-postfix-test"); err != nil || os.Geteuid() != 0 {
		t.Fatal("requires disposable Postfix test container")
	}
	configureTestPostfix(t)
	mx, err := net.Listen("tcp", "127.0.0.1:2526")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mx.Close() })
	var stopDeferring atomic.Bool
	go func() {
		for {
			conn, err := mx.Accept()
			if err != nil {
				return
			}
			go func() { _ = testMXSession(conn, stopDeferring.Load) }()
		}
	}()
	var unavailable, attempted atomic.Bool
	unavailable.Store(true)
	var mu sync.Mutex
	events := map[string]Report{}
	kinds := map[string]string{}
	listener, err := net.Listen("unix", DefaultSocket)
	if err != nil {
		t.Fatal(err)
	}
	group, err := user.LookupGroup("faas")
	if err != nil {
		t.Fatal(err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(DefaultSocket, 0, gid); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(DefaultSocket, 0660); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == MonitoringPath && r.Header.Get("Authorization") == "Bearer "+reportToken {
			_, _ = io.WriteString(w, "# TYPE maild_mail_enabled gauge\nmaild_mail_enabled 1\n# TYPE maild_mail_stats_up gauge\nmaild_mail_stats_up 1\n")
			return
		}
		attempted.Store(true)
		if unavailable.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		report, parseErr := ParsePostfixReport(r.Header.Get(ReportRecipientHeader), raw)
		if err != nil || parseErr != nil || r.URL.Path != PostfixReportPath || r.Header.Get("Authorization") != "Bearer "+reportToken || r.Header.Get(ReportSenderHeader) != "<>" {
			t.Errorf("invalid real Postfix report: read=%v parse=%v", err, parseErr)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		events[report.EventID] = report
		kinds[report.RecipientID] = report.Kind
		mu.Unlock()
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	postfixCommand(t, "postfix", "start")
	t.Cleanup(func() { _ = exec.Command("postfix", "stop").Run() })
	waitPostfix(t, "submission listener", func() bool {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:2525", 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
		}
		return err == nil
	})
	ids := []string{dsnRecipient, "81a25d88-9a8e-425f-9e9b-b28802234faa", "91a25d88-9a8e-425f-9e9b-b28802234faa"}
	for n, address := range []string{"one@example.net", "missing@example.net", "delay@example.net"} {
		state, code := (SMTP{Address: "127.0.0.1:2525", Hello: "test.example.test", RequestDSN: true}).Submit(t.Context(), Envelope{
			From: "bounce+" + ids[n] + "@example.test", To: address, RecipientID: ids[n],
			Data: []byte("From: notify@example.test\r\nTo: " + address + "\r\nSubject: local fixture\r\n\r\nlocal test\r\n"),
		})
		if state != "submitted" {
			t.Fatal("Postfix submission failed", state, code)
		}
	}
	waitPostfix(t, "deferred pipe delivery", attempted.Load)
	// The optional monitoring unit uses faas:postdrop and NoNewPrivileges. Queue
	// inspection must work under those exact restrictions, including while
	// the report pipe is retrying, rather than relying on root test access.
	faasUser, err := user.Lookup("faas")
	if err != nil {
		t.Fatal(err)
	}
	uid, err := strconv.ParseUint(faasUser.Uid, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	postdrop, err := user.LookupGroup("postdrop")
	if err != nil {
		t.Fatal(err)
	}
	queueGID, err := strconv.ParseUint(postdrop.Gid, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	probe := exec.CommandContext(t.Context(), executable, "-test.run=^TestPostfixQueueInventoryAsFaas$", "-test.v")
	probe.Env = append(os.Environ(), "FAAS_TEST_POSTFIX_QUEUE_CHILD=1")
	probe.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(queueGID), Groups: []uint32{uint32(gid)}}}
	if output, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("unprivileged queue inventory failed: %v\n%s", err, output)
	}
	// A failed helper must leave notifications in the persistent Postfix queue
	// across a real queue-manager restart. No log file tail or cursor is needed.
	postfixCommand(t, "postfix", "stop")
	unavailable.Store(false)
	postfixCommand(t, "postfix", "start")
	postfixCommand(t, "postqueue", "-f")
	waitPostfix(t, "acceptance, bounce and deferral reports after restart", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return kinds[ids[0]] == "accepted" && kinds[ids[1]] == "bounced" && kinds[ids[2]] == "deferred"
	})
	stopDeferring.Store(true)
	postfixCommand(t, "postqueue", "-f")
	waitPostfix(t, "relay retry acceptance", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return kinds[ids[2]] == "accepted"
	})
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 4 {
		t.Fatalf("want four distinct durable observations, got %d", len(events))
	}
}

func TestPostfixQueueInventoryAsFaas(t *testing.T) {
	if os.Getenv("FAAS_TEST_POSTFIX_QUEUE_CHILD") != "1" {
		t.Skip("executed by the isolated Postfix fixture")
	}
	if os.Geteuid() == 0 {
		t.Fatal("queue probe retained root privileges")
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	waitPostfix(t, "unprivileged report queue inventory", func() bool {
		queues, err := ReadPostfixQueues(t.Context())
		return err == nil && queues.Reports.Count > 0
	})
	bridge, err := NewPostfixBridge(DefaultSocket, reportToken, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	output := "/tmp/internal-mail-queue-probe.prom"
	defer func() { _ = os.Remove(output) }()
	if err := bridge.ExportMonitoring(t.Context(), output); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(output)
	if err != nil || !bytes.Contains(body, []byte("maild_mail_postfix_stats_up 1")) || !bytes.Contains(body, []byte("maild_mail_monitoring_up 1")) {
		t.Fatal("hardened telemetry export failed", err)
	}
}

func configureTestPostfix(t *testing.T) {
	t.Helper()
	for _, dir := range []string{"/run/faas", "/etc/faas/mail-reports"} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writePostfixFile(t, "/etc/faas/mail-reports/bridge.env", "FAAS_INTERNAL_MAIL_ENABLED=1\nFAAS_INTERNAL_MAIL_REPORTS_ENABLED=1\nFAAS_INTERNAL_MAIL_REPORT_TOKEN="+reportToken+"\n")
	raw, err := os.ReadFile("/fixtures/postfix-main.cf.example")
	if err != nil {
		t.Fatal(err)
	}
	main := strings.ReplaceAll(string(raw), "mail.example.com", "mail.example.test")
	main = strings.ReplaceAll(main, "192.0.2.10", "127.0.0.1")
	main = strings.ReplaceAll(main, "delay_warning_time = 1h", "delay_warning_time = 1s")
	main += "\nsmtp_tls_security_level = none\nqueue_run_delay = 1s\nminimal_backoff_time = 1s\nmaximal_backoff_time = 2s\n" +
		"default_transport = error:Internet delivery disabled in fixture\nmaillog_file = /var/log/postfix.log\n"
	writePostfixFile(t, "/etc/postfix/main.cf", main)
	raw, err = os.ReadFile("/fixtures/postfix-master.cf.example")
	if err != nil {
		t.Fatal(err)
	}
	master, err := os.ReadFile("/etc/postfix/master.cf")
	if err != nil {
		t.Fatal(err)
	}
	// Disable the distribution's port-25 listener; all submissions use the
	// example's fixed loopback port. Unix Postfix services retain defaults.
	master = bytes.ReplaceAll(master, []byte("smtp      inet"), []byte("#smtp      inet"))
	writePostfixFile(t, "/etc/postfix/master.cf", string(master)+"\n"+string(raw))
	raw, err = os.ReadFile("/fixtures/postfix-report-transports.example")
	if err != nil {
		t.Fatal(err)
	}
	routes := strings.ReplaceAll(string(raw), `example\.com`, `example\.test`)
	routes += "/@example\\.net$/ smtp:[127.0.0.1]:2526\n"
	writePostfixFile(t, "/etc/postfix/gregale-report-transports", routes)
}

func writePostfixFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func postfixCommand(t *testing.T, name string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", name, err, output)
	}
}

func waitPostfix(t *testing.T, description string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	log, _ := os.ReadFile("/var/log/postfix.log")
	t.Fatalf("timed out waiting for %s\n%s", description, log)
}

func testMXSession(conn net.Conn, stopDeferring func() bool) error {
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	p := textproto.NewConn(conn)
	if err := p.PrintfLine("220 mx.example.net ESMTP"); err != nil {
		return err
	}
	for {
		line, err := p.ReadLine()
		if err != nil {
			return err
		}
		reply := "250 2.0.0 OK"
		switch {
		case strings.HasPrefix(line, "EHLO "):
			// Advertise DSN: our relay must discard it and generate its own
			// handoff notification, rather than waiting for this fake peer.
			reply = "250-mx.example.net\r\n250 DSN"
		case strings.HasPrefix(line, "MAIL FROM:"):
			if strings.Contains(line, "ENVID=") {
				return fmt.Errorf("relay delegated DSN reporting: %s", line)
			}
		case strings.HasPrefix(line, "RCPT TO:<missing@"):
			reply = "550 5.1.1 mailbox unavailable"
		case strings.HasPrefix(line, "RCPT TO:<delay@"):
			if !stopDeferring() {
				reply = "450 4.2.0 mailbox temporarily unavailable"
			}
		case line == "DATA":
			if err := p.PrintfLine("354 send data"); err != nil {
				return err
			}
			if _, err := io.Copy(io.Discard, p.DotReader()); err != nil {
				return err
			}
		case line == "QUIT":
			return p.PrintfLine("221 bye")
		}
		if err := p.PrintfLine("%s", reply); err != nil {
			return err
		}
	}
}

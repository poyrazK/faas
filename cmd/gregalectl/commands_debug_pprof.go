// commands_debug_pprof.go — `gregalectl debug pprof` (ADR-20261023200002).
//
// Captures a Go profile from a platform daemon on this host through its
// root-only abstract socket @faas-debug/<daemon> (pkg/wire/debugprofile.go)
// and writes it to a file for `go tool pprof`. Run it as root on the
// daemon's host: the daemon accepts only root and its own uid.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

const subDebugPprof = "pprof"

// Indirections for tests: the platform, the socket dialer and the kernel's
// unix socket table.
var (
	debugPprofGOOS = runtime.GOOS
	debugPprofDial = func(ctx context.Context, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", "@"+addr)
	}
	debugPprofProcNetUnix = "/proc/net/unix"
)

// debugPprofProfiles maps the --profile value to its endpoint.
var debugPprofProfiles = map[string]string{
	"cpu": "profile", "profile": "profile", "trace": "trace",
	"heap": "heap", "allocs": "allocs", "goroutine": "goroutine",
	"threadcreate": "threadcreate", "block": "block", "mutex": "mutex",
}

type debugPprofOptions struct {
	daemon, profile, out string
	pid, seconds, debug  int
	gc                   bool
}

func cmdDebugPprof(args []string) int {
	fs := flag.NewFlagSet("debug pprof", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	var o debugPprofOptions
	fs.StringVar(&o.daemon, "daemon", "", "daemon to profile (schedd, apid, vmmd, gatewayd-internal, …)")
	fs.IntVar(&o.pid, "pid", 0, "the daemon's pid, when two generations run (see --list)")
	fs.StringVar(&o.profile, "profile", "heap", "cpu, trace, heap, allocs, goroutine, threadcreate, block or mutex")
	fs.IntVar(&o.seconds, "seconds", 30, "cpu and trace: how long to record (1..60)")
	fs.IntVar(&o.debug, "debug", 0, "named profiles: 0 binary for go tool pprof, 1 or 2 text (goroutine 2 = full dump)")
	fs.BoolVar(&o.gc, "gc", false, "heap: run a GC first, so the profile shows live memory")
	fs.StringVar(&o.out, "o", "", "output file (default <daemon>-<profile>-<time>.<ext>)")
	list := fs.Bool("list", false, "list the daemons serving profiles on this host")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if debugPprofGOOS != "linux" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl debug pprof: run this as root on the daemon's Linux host; profiles are served on a root-only abstract socket")
		return 2
	}
	if *list {
		return debugPprofList()
	}
	path, err := o.endpoint(fs.NArg())
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl debug pprof: %v\n", err)
		return 2
	}
	if o.out == "" {
		o.out = o.defaultOutput(time.Now())
	}
	if err := debugPprofFetch(o, path); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl debug pprof: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(osStdout, "wrote %s\n", o.out)
	if o.debug == 0 && o.profile != "trace" {
		_, _ = fmt.Fprintf(osStdout, "inspect: go tool pprof -http=:0 %s\n", o.out)
	} else if o.profile == "trace" {
		_, _ = fmt.Fprintf(osStdout, "inspect: go tool trace %s\n", o.out)
	}
	return 0
}

// endpoint validates the options and returns the request path and query.
func (o *debugPprofOptions) endpoint(nargs int) (string, error) {
	if nargs != 0 {
		return "", errors.New("no positional arguments")
	}
	if o.daemon == "" || o.daemon != strings.TrimSpace(o.daemon) || strings.ContainsAny(o.daemon, "/ ") {
		return "", errors.New("--daemon is required (see --list)")
	}
	name, ok := debugPprofProfiles[o.profile]
	if !ok {
		return "", fmt.Errorf("unknown --profile %q", o.profile)
	}
	q := url.Values{}
	switch name {
	case "profile", "trace":
		if o.seconds < 1 || o.seconds > wire.DebugProfileMaxSeconds {
			return "", fmt.Errorf("--seconds must be 1..%d", wire.DebugProfileMaxSeconds)
		}
		q.Set("seconds", strconv.Itoa(o.seconds))
	default:
		if o.debug < 0 || o.debug > 2 {
			return "", errors.New("--debug must be 0, 1 or 2")
		}
		q.Set("debug", strconv.Itoa(o.debug))
		if o.gc && name == "heap" {
			q.Set("gc", "1")
		}
	}
	return "/debug/pprof/" + name + "?" + q.Encode(), nil
}

func (o *debugPprofOptions) socket() string {
	if o.pid > 0 {
		return fmt.Sprintf("%s%s.%d", wire.DebugProfileSocketPrefix, o.daemon, o.pid)
	}
	return wire.DebugProfileSocketPrefix + o.daemon
}

func (o *debugPprofOptions) defaultOutput(now time.Time) string {
	ext := ".pb.gz"
	switch {
	case o.profile == "trace":
		ext = ".trace"
	case debugPprofProfiles[o.profile] != "profile" && o.debug > 0:
		ext = ".txt"
	}
	return fmt.Sprintf("%s-%s-%s%s", o.daemon, o.profile, now.UTC().Format("20060102T150405Z"), ext)
}

func debugPprofFetch(o debugPprofOptions, path string) error {
	socket := o.socket()
	client := &http.Client{
		Timeout: time.Duration(o.seconds+30) * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return debugPprofDial(ctx, socket)
		}},
	}
	resp, err := client.Get("http://faas-debug" + path)
	if err != nil {
		return fmt.Errorf("connect to @%s: %w (is the daemon running here, and are you root?)", socket, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("@%s answered %d: %s", socket, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	f, err := os.OpenFile(o.out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", o.out, err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", o.out, err)
	}
	return f.Close()
}

// debugPprofList prints the daemons whose profiling socket is listening,
// read from the kernel's unix socket table.
func debugPprofList() int {
	raw, err := os.ReadFile(debugPprofProcNetUnix)
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl debug pprof: read %s: %v\n", debugPprofProcNetUnix, err)
		return 1
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if path := fields[len(fields)-1]; strings.HasPrefix(path, "@"+wire.DebugProfileSocketPrefix) {
			seen[strings.TrimPrefix(path, "@"+wire.DebugProfileSocketPrefix)] = true
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl debug pprof: no daemon on this host serves profiles")
		return 1
	}
	for _, n := range names {
		daemon, pid, _ := strings.Cut(n, ".")
		if pid != "" {
			_, _ = fmt.Fprintf(osStdout, "%s (--pid %s)\n", daemon, pid)
		} else {
			_, _ = fmt.Fprintln(osStdout, daemon)
		}
	}
	return 0
}

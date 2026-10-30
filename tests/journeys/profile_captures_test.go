package journeys

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestProfileCaptures is the on-demand profile capture journey (ADR-1155):
// capture CPU and heap from the running probe while it serves traffic, read
// the capture back, compare two captures, download the merged pprof files,
// delete a capture, and see a request held past the slow-request threshold
// produce an automatic capture. profile-captures is a preview released per
// account: the journey skips on accounts it is not released to and on
// installations without on-demand profiling.
func TestProfileCaptures(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	skipHidden(t, hiddenCapabilities(t, tg), "profile-captures")
	slug, base := tg.deployProbe(t, "captures")
	s := newStory(t)
	var first, second string

	s.step("capture", func(t *testing.T) {
		first = tg.captureUnderLoad(t, slug, base, "cpu,heap")
	})
	s.check("capture_listed", func(t *testing.T) {
		list := object(t, tg.ok(t, time.Minute, "debug", "captures", slug).stdout)
		captures, _ := field(list, "captures").([]any)
		for _, c := range captures {
			if str(c, "id") == first {
				if str(c, "status") != "ready" || str(c, "trigger") != "manual" {
					t.Fatalf("listed capture: %s", compact(c))
				}
				return
			}
		}
		t.Fatalf("capture %s not listed: %s", first, compact(list))
	})
	s.check("pprof_download", func(t *testing.T) {
		dir := t.TempDir()
		tg.ok(t, 2*time.Minute, "debug", "captures", slug, first, "--output", dir)
		for _, kind := range []string{"cpu", "heap"} {
			raw, err := os.ReadFile(filepath.Join(dir, "profile-"+first+"-"+kind+".pb.gz"))
			if err != nil || !bytes.HasPrefix(raw, []byte{0x1f, 0x8b}) {
				t.Fatalf("%s pprof file: %d bytes, %v", kind, len(raw), err)
			}
		}
	})
	s.check("capture_diff", func(t *testing.T) {
		second = tg.captureUnderLoad(t, slug, base, "heap")
		diff := object(t, tg.ok(t, 2*time.Minute, "debug", "capture-diff", slug, second, "--base", first).stdout)
		if str(diff, "kind") != "heap" || str(diff, "unit") != "bytes" {
			t.Fatalf("diff: %s", compact(diff))
		}
	})
	s.step("delete", func(t *testing.T) {
		doc := object(t, tg.ok(t, time.Minute, "debug", "captures", slug, first, "--delete").stdout)
		if deleted, _ := field(doc, "deleted").(bool); !deleted {
			t.Fatalf("delete: %s", compact(doc))
		}
		res := tg.run(t, time.Minute, "debug", "captures", slug, first)
		if res.exit == 0 || res.problemCode() != "not_found" {
			t.Fatalf("deleted capture still readable: exit %d, %s", res.exit, res.problem())
		}
	})
	s.check("slow_request_captured", func(t *testing.T) {
		// A request in flight for 10 s makes the gateway report it; schedd
		// queues a stacks and CPU capture of the serving instance.
		if r := get(base + "/slow?ms=13000"); r.status != 200 {
			t.Fatalf("slow request: %s", r.String())
		}
		eventually(t, "an automatic slow-request capture", 3*time.Minute, 5*time.Second, func() (bool, string) {
			list := object(t, tg.ok(t, time.Minute, "debug", "captures", slug).stdout)
			captures, _ := field(list, "captures").([]any)
			for _, c := range captures {
				if str(c, "trigger") == "slow_request" {
					return str(c, "status") == "ready" || str(c, "status") == "failed", compact(c)
				}
			}
			return false, compact(list)
		})
	})
}

// captureUnderLoad runs one on-demand capture of the probe while requests
// keep it busy, and returns the ready capture's id. It skips when the
// installation has on-demand profiling off: the documented answer, not a
// platform failure.
func (tg *target) captureUnderLoad(t *testing.T, slug, base, kinds string) string {
	t.Helper()
	// Captures never wake a parked app, so wake it first.
	eventually(t, "the probe serves", 2*time.Minute, 2*time.Second, func() (bool, string) {
		r := get(base + "/")
		return r.status == 200, r.String()
	})
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				get(base + "/")
			}
		}
	}()
	res := tg.run(t, 3*time.Minute, "debug", "capture", slug, "--type", kinds, "--duration", "5s")
	close(stop)
	<-done
	if strings.Contains(res.problem(), "not enabled on this installation") {
		t.Skipf("on-demand profiling is off on this installation: %s", res.problem())
	}
	if res.exit != 0 {
		t.Fatalf("capture failed: exit %d\nstdout: %s\nstderr: %s", res.exit, res.stdout, res.problem())
	}
	doc := object(t, res.stdout)
	if str(doc, "capture.status") != "ready" || num(doc, "capture.processes") < 1 {
		t.Fatalf("capture: %s", compact(field(doc, "capture")))
	}
	for _, kind := range strings.Split(kinds, ",") {
		if field(doc, "views."+kind) == nil {
			t.Fatalf("no %s view: %s", kind, compact(doc))
		}
	}
	if strings.Contains(kinds, "cpu") {
		if empty, _ := field(doc, "views.cpu.empty").(bool); empty {
			t.Errorf("the CPU view is empty although requests kept the probe busy: %s", compact(field(doc, "capture")))
		}
	}
	return str(doc, "capture.id")
}

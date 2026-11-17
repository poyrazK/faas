//go:build metal

package fcvm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestMetalWarmVsInitFirstRequest measures what the warm snapshot tier
// (ADR-055/071) buys over an init snapshot. For each runtime it cold-boots one
// app, captures an init snapshot right after readiness (as the deployment
// prime does), serves warmVsInitWarmup requests, captures a warm snapshot (as
// a Pro/Scale park does), then restores each snapshot repeatedly and times the
// wake and the first two requests, with the host page cache warm and dropped.
// It reports; it asserts nothing about which tier wins.
//
//	FAAS_RUN_WARM_INIT_BENCH=1 FAAS_TEST_KERNEL=<release vmlinux> \
//	FAAS_TEST_RNG_GO_ROOTFS=/var/tmp/alpine.tar FAAS_TEST_RNG_NODE_ROOTFS=/var/tmp/node22.tar \
//	FAAS_TEST_RNG_PYTHON_ROOTFS=/var/tmp/python3-debian.tar FAAS_TEST_RNG_JAVA_ROOTFS=/var/tmp/temurin21-jdk.tar \
//	make test-metal PKGS=./pkg/fcvm RUN_REGEX=TestMetalWarmVsInitFirstRequest
//
// FAAS_WARM_INIT_BENCH_OUT names a JSON file for the raw samples.
func TestMetalWarmVsInitFirstRequest(t *testing.T) {
	if os.Getenv("FAAS_RUN_WARM_INIT_BENCH") != "1" {
		t.Skip("set FAAS_RUN_WARM_INIT_BENCH=1 to run the warm-vs-init snapshot benchmark")
	}
	type app struct {
		name, rootfsEnv, file string
		content               func(*testing.T) string
		entrypoint            []string
	}
	apps := []app{
		{"go", "FAAS_TEST_RNG_GO_ROOTFS", "srv/probe", func(t *testing.T) string { return compatGoProbe(t, warmBenchGoApp) }, []string{"/srv/probe"}},
		{"node", "FAAS_TEST_RNG_NODE_ROOTFS", "srv/app.js", func(*testing.T) string { return warmBenchNodeApp }, []string{"/usr/local/bin/node", "/srv/app.js"}},
		{"python", "FAAS_TEST_RNG_PYTHON_ROOTFS", "srv/app.py", func(*testing.T) string { return warmBenchPythonApp }, []string{"/usr/bin/python3", "/srv/app.py"}},
		{"java", "FAAS_TEST_RNG_JAVA_ROOTFS", "srv/BenchApp.java", func(*testing.T) string { return warmBenchJavaApp }, []string{"/opt/java/openjdk/bin/java", "/srv/BenchApp.java"}},
	}
	var report []warmBenchResult
	for _, a := range apps {
		tarball := os.Getenv(a.rootfsEnv)
		if tarball == "" {
			t.Logf("%s: %s unset, skipped", a.name, a.rootfsEnv)
			continue
		}
		t.Run(a.name, func(t *testing.T) {
			report = append(report, runWarmVsInit(t, a.name, tarball, a.file, a.content(t), a.entrypoint)...)
		})
	}
	for _, r := range report {
		t.Logf("SUMMARY %-6s %-4s cache=%-4s n=%d stored=%dMiB mem=%dMiB wake p50=%v max=%v | 1st p50=%v max=%v | 2nd p50=%v | wake+1st p50=%v",
			r.Runtime, r.Tier, r.Cache, len(r.WakeMs), r.StoredBytes>>20, r.MemBytes>>20,
			ms(median(r.WakeMs)), ms(slices.Max(r.WakeMs)), ms(median(r.FirstMs)), ms(slices.Max(r.FirstMs)),
			ms(median(r.SecondMs)), ms(median(sum(r.WakeMs, r.FirstMs))))
	}
	if out := os.Getenv("FAAS_WARM_INIT_BENCH_OUT"); out != "" {
		data, _ := json.MarshalIndent(report, "", " ")
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Errorf("write %s: %v", out, err)
		}
	}
}

const (
	warmVsInitWarmup   = 50
	warmVsInitHot      = 7
	warmVsInitColdRuns = 4
)

type warmBenchResult struct {
	Runtime, Tier, Cache      string
	StoredBytes, MemBytes     int64
	WakeMs, FirstMs, SecondMs []float64
}

func runWarmVsInit(t *testing.T, name, tarball, file, content string, entrypoint []string) []warmBenchResult {
	p, inst := startCompatProbe(t, "wvi-"+name, tarball, file, content, entrypoint, map[string]string{}, func(v *JailerVMM) {
		// Mirror production vmmd's restore path.
		v.WithRestorePrefetch(true)
		if fc, err := DetectFirecrackerVersion(context.Background()); err == nil && ClockRealtimeSupported(fc) && HostClocksourceTSC() {
			v.WithRestoreClockRealtime(true)
		}
	})
	capture := func(tier string) (*Snapshot, SnapshotInfo) {
		t.Helper()
		snap := &Snapshot{
			FCVersion:         p.fcVersion,
			StorageKey:        fmt.Sprintf("snap/%s/%s/mem", p.instance, tier),
			VMStateStorageKey: fmt.Sprintf("snap/%s/%s/vmstate", p.instance, tier),
			VMStatePath:       filepath.Join(t.TempDir(), "vmstate"),
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		info, err := p.m.WarmSnapshot(ctx, p.instance, SnapshotSpec{
			VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey,
		})
		if err != nil {
			t.Fatalf("%s capture: %v", tier, err)
		}
		return snap, info
	}
	// Init: captured right after readiness, before any request.
	initSnap, initInfo := capture("init")
	for i := 0; i < warmVsInitWarmup; i++ {
		if _, err := timedGet(inst.Lease.HostIP); err != nil {
			t.Fatalf("warm-up request %d: %v", i, err)
		}
	}
	warmSnap, warmInfo := capture("warm")
	if err := p.m.Destroy(context.Background(), p.instance); err != nil {
		t.Fatalf("destroy after captures: %v", err)
	}

	var out []warmBenchResult
	for _, cache := range []string{"hot", "cold"} {
		for _, tier := range []struct {
			name string
			snap *Snapshot
			info SnapshotInfo
		}{{"init", initSnap, initInfo}, {"warm", warmSnap, warmInfo}} {
			r := warmBenchResult{Runtime: name, Tier: tier.name, Cache: cache, StoredBytes: tier.info.StoredBytes, MemBytes: tier.info.MemBytes}
			runs := warmVsInitHot
			if cache == "cold" {
				runs = warmVsInitColdRuns
			}
			for i := 0; i < runs; i++ {
				if cache == "cold" {
					dropPageCache(t)
				}
				p.snap = tier.snap
				start := time.Now()
				woken := p.restore()
				wake := time.Since(start)
				first, err := timedGet(woken.Lease.HostIP)
				if err != nil {
					t.Fatalf("%s/%s/%s first request: %v", name, tier.name, cache, err)
				}
				second, err := timedGet(woken.Lease.HostIP)
				if err != nil {
					t.Fatalf("%s/%s/%s second request: %v", name, tier.name, cache, err)
				}
				if err := p.m.Destroy(context.Background(), p.instance); err != nil {
					t.Fatalf("destroy: %v", err)
				}
				r.WakeMs = append(r.WakeMs, msf(wake))
				r.FirstMs = append(r.FirstMs, msf(first))
				r.SecondMs = append(r.SecondMs, msf(second))
			}
			t.Logf("%s %s cache=%s wake=%v first=%v second=%v", name, tier.name, cache, r.WakeMs, r.FirstMs, r.SecondMs)
			out = append(out, r)
		}
	}
	return out
}

// timedGet times one request on a fresh connection, as the gateway's first
// request to a woken instance is.
func timedGet(host netip.Addr) (time.Duration, error) {
	client := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	start := time.Now()
	resp, err := client.Get(fmt.Sprintf("http://%s:8080/work", host))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	return time.Since(start), nil
}

func msf(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func ms(v float64) time.Duration {
	return time.Duration(v * float64(time.Millisecond)).Round(100 * time.Microsecond)
}

func median(v []float64) float64 {
	s := slices.Clone(v)
	slices.Sort(s)
	if len(s) == 0 {
		return 0
	}
	return s[len(s)/2]
}

func sum(a, b []float64) []float64 {
	out := make([]float64, len(a))
	for i := range a {
		out[i] = a[i] + b[i]
	}
	return out
}

// Every app serves /work with the same shape of work a small API request
// does: build 300 records, filter them with a regex, render a template, encode
// JSON, hash it and gzip it. Node and Python load those modules on the first
// request, as frameworks commonly do.

const warmBenchGoApp = `package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"text/template"
)

type rec struct {
	ID    int      ` + "`json:\"id\"`" + `
	Name  string   ` + "`json:\"name\"`" + `
	Tags  []string ` + "`json:\"tags\"`" + `
	Price float64  ` + "`json:\"price\"`" + `
}

var tmpl = template.Must(template.New("t").Parse("{{range .}}<li>{{.Name}} {{.Price}}</li>{{end}}"))
var re = regexp.MustCompile("item-[0-9]*7$")

func main() {
	http.HandleFunc("/work", func(w http.ResponseWriter, _ *http.Request) {
		recs := make([]rec, 0, 300)
		for i := 0; i < 300; i++ {
			recs = append(recs, rec{i, fmt.Sprintf("item-%d", i), []string{"a", "b", strings.Repeat("c", i%7)}, float64(i) * 1.25})
		}
		var hit []rec
		for _, r := range recs {
			if re.MatchString(r.Name) {
				hit = append(hit, r)
			}
		}
		var html bytes.Buffer
		_ = tmpl.Execute(&html, hit)
		body, _ := json.Marshal(recs)
		sum := sha256.Sum256(body)
		var gz bytes.Buffer
		zw := gzip.NewWriter(&gz)
		_, _ = zw.Write(body)
		_ = zw.Close()
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"hits": len(hit), "html": html.Len(), "gz": gz.Len(), "sha": hex.EncodeToString(sum[:])})
	})
	_ = http.ListenAndServe("0.0.0.0:8080", nil)
}
`

const warmBenchNodeApp = `const http = require('http');
http.createServer((req, res) => {
  const zlib = require('zlib');
  const crypto = require('crypto');
  const recs = [];
  for (let i = 0; i < 300; i++) recs.push({ id: i, name: 'item-' + i, tags: ['a', 'b', 'c'.repeat(i % 7)], price: i * 1.25 });
  const hit = recs.filter(r => /item-[0-9]*7$/.test(r.name));
  const html = hit.map(r => '<li>' + r.name + ' ' + r.price + '</li>').join('');
  const body = JSON.stringify(recs);
  const sha = crypto.createHash('sha256').update(body).digest('hex');
  const gz = zlib.gzipSync(body);
  res.setHeader('content-type', 'application/json');
  res.end(JSON.stringify({ hits: hit.length, html: html.length, gz: gz.length, sha }));
}).listen(8080, '0.0.0.0');
`

const warmBenchPythonApp = `import http.server
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        import gzip, hashlib, json, re, string
        recs = [{"id": i, "name": "item-%d" % i, "tags": ["a", "b", "c" * (i % 7)], "price": i * 1.25} for i in range(300)]
        pat = re.compile(r"item-[0-9]*7$")
        hit = [r for r in recs if pat.search(r["name"])]
        tmpl = string.Template("<li>$name $price</li>")
        html = "".join(tmpl.substitute(name=r["name"], price=r["price"]) for r in hit)
        body = json.dumps(recs).encode()
        sha = hashlib.sha256(body).hexdigest()
        gz = gzip.compress(body)
        out = json.dumps({"hits": len(hit), "html": len(html), "gz": len(gz), "sha": sha}).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)
    def log_message(self, *args):
        pass
http.server.ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
`

const warmBenchJavaApp = `import com.sun.net.httpserver.HttpServer;
import java.io.ByteArrayOutputStream;
import java.net.InetSocketAddress;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.List;
import java.util.regex.Pattern;
import java.util.zip.GZIPOutputStream;

public class BenchApp {
    record Rec(int id, String name, String tag, double price) {}
    static final Pattern RE = Pattern.compile("item-[0-9]*7$");

    public static void main(String[] args) throws Exception {
        HttpServer server = HttpServer.create(new InetSocketAddress("0.0.0.0", 8080), 0);
        server.createContext("/work", exchange -> {
            try {
                List<Rec> recs = new ArrayList<>();
                for (int i = 0; i < 300; i++) recs.add(new Rec(i, "item-" + i, "c".repeat(i % 7), i * 1.25));
                StringBuilder html = new StringBuilder();
                int hits = 0;
                for (Rec r : recs) if (RE.matcher(r.name()).find()) { hits++; html.append("<li>").append(r.name()).append(' ').append(r.price()).append("</li>"); }
                StringBuilder json = new StringBuilder("[");
                for (Rec r : recs) json.append("{\"id\":").append(r.id()).append(",\"name\":\"").append(r.name()).append("\",\"tags\":[\"a\",\"b\",\"").append(r.tag()).append("\"],\"price\":").append(r.price()).append("},");
                json.setCharAt(json.length() - 1, ']');
                byte[] body = json.toString().getBytes();
                String sha = HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(body));
                ByteArrayOutputStream gz = new ByteArrayOutputStream();
                try (GZIPOutputStream z = new GZIPOutputStream(gz)) { z.write(body); }
                byte[] out = ("{\"hits\":" + hits + ",\"html\":" + html.length() + ",\"gz\":" + gz.size() + ",\"sha\":\"" + sha + "\"}").getBytes();
                exchange.getResponseHeaders().add("content-type", "application/json");
                exchange.sendResponseHeaders(200, out.length);
                exchange.getResponseBody().write(out);
            } catch (Exception e) {
                exchange.sendResponseHeaders(500, -1);
            } finally {
                exchange.close();
            }
        });
        server.start();
    }
}
`

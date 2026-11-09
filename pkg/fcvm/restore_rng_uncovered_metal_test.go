//go:build metal

// adr: 20261107160000
package fcvm

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// spec: §6.2 invariant 5. ADR-680 reseeds Node and Python userspace generators
// on restore and lists Go, Java, Ruby and PHP as not covered.
// TestMetalRestoreUncoveredRuntimeRNG measures those runtimes so the docs
// state evidence instead of an assumption (ADR-20261107160000).
//
// Each probe draws from every in-process generator at startup, so their state
// is in the snapshot, and reports fresh draws per request. Fields in mustDiffer
// read the kernel on every call and must differ across two restores of one
// snapshot: a repeat there is an invariant 5 failure. Fields in known are the
// documented gap; the test logs whether each replayed and never fails on them.
//
//	FAAS_TEST_RNG_GO_ROOTFS=/var/tmp/alpine.tar \
//	FAAS_TEST_RNG_JAVA_ROOTFS=/var/tmp/temurin21.tar \
//	make test-metal PKGS=./pkg/fcvm RUN_REGEX=TestMetalRestoreUncoveredRuntimeRNG
//
// The Go probe is compiled here (static, CGO off) and copied into any rootfs;
// the Java rootfs needs a JDK 21 at /opt/java/openjdk (`docker export` of
// eclipse-temurin:21-jdk-alpine).
func TestMetalRestoreUncoveredRuntimeRNG(t *testing.T) {
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if kernel == "" {
		kernel = "/srv/fc/base/vmlinux-6.1.134"
	}
	if _, err := os.Stat(kernel); err != nil {
		t.Skipf("kernel %s unavailable: %v (set FAAS_TEST_KERNEL)", kernel, err)
	}
	type probe struct {
		name, rootfsEnv, file string
		content               func(t *testing.T) string
		entrypoint            []string
		mustDiffer, known     []string
	}
	probes := []probe{
		{
			name: "go", rootfsEnv: "FAAS_TEST_RNG_GO_ROOTFS", file: "srv/rng-probe",
			content: buildGoRNGProbe, entrypoint: []string{"/srv/rng-probe"},
			mustDiffer: []string{"crypto"}, known: []string{"math", "mathv2"},
		},
		{
			name: "java", rootfsEnv: "FAAS_TEST_RNG_JAVA_ROOTFS", file: "srv/RngApp.java",
			content: func(*testing.T) string { return rngJavaApp }, entrypoint: []string{"/opt/java/openjdk/bin/java", "/srv/RngApp.java"},
			mustDiffer: []string{}, known: []string{"random", "secure", "math"},
		},
	}
	ran := 0
	for _, p := range probes {
		tarball := os.Getenv(p.rootfsEnv)
		if tarball == "" {
			continue
		}
		ran++
		t.Run(p.name, func(t *testing.T) {
			first, second := restoreSnapshotTwice(t, kernel, tarball, p.file, p.content(t), p.entrypoint, map[string]string{}, false)
			t.Logf("restore 1: %v", first)
			t.Logf("restore 2: %v", second)
			for _, field := range p.mustDiffer {
				if first[field] == nil || first[field] == second[field] {
					t.Errorf("%s: %s = %v after both restores of one snapshot; a generator that reads the kernel replayed", p.name, field, first[field])
				}
			}
			for _, field := range p.known {
				if first[field] == second[field] {
					t.Logf("%s: %s REPLAYED across restores (known gap, ADR-680 not covered)", p.name, field)
				} else {
					t.Logf("%s: %s differed across restores", p.name, field)
				}
			}
		})
	}
	if ran == 0 {
		t.Skip("set FAAS_TEST_RNG_GO_ROOTFS and/or FAAS_TEST_RNG_JAVA_ROOTFS to a root filesystem tarball")
	}
}

const rngGoProbe = `package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	mrand "math/rand"
	mrand2 "math/rand/v2"
	"net/http"
	"os"
	"strconv"
)

func main() {
	mrand.Int63()
	mrand2.Uint64()
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"crypto": hex.EncodeToString(b),
			"math":   strconv.FormatInt(mrand.Int63(), 10),
			"mathv2": strconv.FormatUint(mrand2.Uint64(), 10),
			"pid":    os.Getpid(),
		})
	})
	_ = http.ListenAndServe("0.0.0.0:8080", nil)
}
`

// buildGoRNGProbe compiles the probe into a static linux binary and returns
// its bytes; buildRuntimeBaseExt4 writes them as the executable entrypoint.
func buildGoRNGProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(rngGoProbe), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module rngprobe\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "rng-probe")
	build := exec.Command("go", "build", "-trimpath", "-o", out, ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build go probe: %v: %s", err, combined)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The single-file source launcher (JDK 11+) compiles at startup; HexFormat
// needs JDK 17+.
const rngJavaApp = `import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.security.SecureRandom;
import java.util.HexFormat;
import java.util.Random;

public class RngApp {
    static final Random RANDOM = new Random();
    static final SecureRandom SECURE = new SecureRandom();

    public static void main(String[] args) throws Exception {
        RANDOM.nextLong();
        SECURE.nextBytes(new byte[16]);
        Math.random();
        HttpServer server = HttpServer.create(new InetSocketAddress("0.0.0.0", 8080), 0);
        server.createContext("/", exchange -> {
            byte[] b = new byte[16];
            SECURE.nextBytes(b);
            String body = "{\"random\":\"" + RANDOM.nextLong() + "\",\"secure\":\"" + HexFormat.of().formatHex(b)
                + "\",\"math\":\"" + Math.random() + "\",\"pid\":" + ProcessHandle.current().pid() + "}";
            byte[] out = body.getBytes();
            exchange.getResponseHeaders().add("content-type", "application/json");
            exchange.sendResponseHeaders(200, out.length);
            exchange.getResponseBody().write(out);
            exchange.close();
        });
        server.start();
    }
}
`

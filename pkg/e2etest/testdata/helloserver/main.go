// hello-server is the e2e image-deploy fixture: the smallest thing that is
// honestly an app.
//
// It is built at test time as a static linux binary and shipped as the ONLY
// content of the fixture image alongside app/hello.txt, so the image is what
// a scratch-based customer image is — no shell, no libc, no /dev — and the
// platform has to run it as such. The previous fixture advertised
// `/bin/sh -c cat app/hello.txt`, which prints a file and exits, never
// listens, and needs a shell the image did not contain; it only ever "ran"
// through a stub base that e2e-native no longer uses.
//
// Modes:
//
//	(default)      serve the contents of -body-file on / and 200 on /healthz;
//	               bind $PORT when -addr is omitted (falling back to 8080)
//	-spin          also burn one CPU forever (cpu-fairness fixture)
//	-ignore-term   ignore SIGTERM (wedged-process fixture)
//	-no-listen     never bind the port, so liveness sees conn_refused
//	-no-healthz    omit /healthz so TCP readiness is the only boot contract
//	-durable-counter serve the pure durable entity counter protocol
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

type processEvidence struct {
	UID              int    `json:"uid"`
	GID              int    `json:"gid"`
	WorkingDir       string `json:"working_dir"`
	Marker           string `json:"marker"`
	DeploymentMarker string `json:"deployment_marker,omitempty"`
	Cgroup           string `json:"cgroup,omitempty"`
	Count            int    `json:"count,omitempty"`
}

func currentEvidence() processEvidence {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	cgroup, _ := os.ReadFile("/proc/self/cgroup")
	return processEvidence{UID: os.Getuid(), GID: os.Getgid(), WorkingDir: cwd,
		Marker: os.Getenv("FIXTURE_MARKER"), DeploymentMarker: os.Getenv("DEPLOYMENT_MARKER"), Cgroup: strings.TrimSpace(string(cgroup))}
}

func main() {
	addr := flag.String("addr", "", "listen address (defaults to $PORT or :8080)")
	bodyFile := flag.String("body-file", "/app/hello.txt", "file whose contents are served on /")
	spin := flag.Bool("spin", false, "burn one CPU forever")
	ignoreTerm := flag.Bool("ignore-term", false, "ignore SIGTERM")
	noListen := flag.Bool("no-listen", false, "never bind the port")
	contract := flag.Bool("contract", false, "expose fixed process-contract evidence")
	probeContract := flag.Bool("probe-contract", false, "report this exec probe process to the local fixture server")
	noHealthz := flag.Bool("no-healthz", false, "omit the /healthz endpoint")
	durableCounter := flag.Bool("durable-counter", false, "serve pure durable entity transitions")
	failAPI := flag.Bool("fail-api", false, "answer /api with 500 while / and /healthz stay healthy (bad release)")
	failLivez := flag.Bool("fail-livez", false, "answer /livez with 500 while /healthz stays healthy (crash loop)")
	flag.Parse()
	if *addr == "" {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		if strings.HasPrefix(port, ":") {
			*addr = port
		} else {
			*addr = ":" + port
		}
	}

	if *probeContract {
		_, port, err := net.SplitHostPort(*addr)
		if err != nil {
			log.Fatal(err)
		}
		body, err := json.Marshal(currentEvidence())
		if err != nil {
			log.Fatal(err)
		}
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Post("http://127.0.0.1:"+port+"/probe-contract", "application/json", bytes.NewReader(body))
		if err != nil {
			log.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			log.Fatalf("probe contract status=%d", resp.StatusCode)
		}
		return
	}

	if *ignoreTerm {
		signal.Ignore(syscall.SIGTERM)
	}
	if *spin {
		go func() {
			for {
			}
		}()
	}
	if *noListen {
		select {}
	}

	body, err := os.ReadFile(*bodyFile)
	if err != nil {
		log.Fatalf("hello-server: read %s: %v", *bodyFile, err)
	}
	body = []byte(strings.TrimRight(string(body), "\n") + "\n")

	mux := http.NewServeMux()
	if *durableCounter {
		mux.HandleFunc("/__gregale/entities", serveDurableCounter)
	}
	if *contract {
		var mu sync.Mutex
		var lastProbe *processEvidence
		mux.HandleFunc("/probe-contract", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			var evidence processEvidence
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&evidence); err != nil {
				http.Error(w, "invalid probe evidence", http.StatusBadRequest)
				return
			}
			mu.Lock()
			evidence.Count = 1
			if lastProbe != nil {
				evidence.Count = lastProbe.Count + 1
			}
			lastProbe = &evidence
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		})
		mux.HandleFunc("/contract", func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			probe := lastProbe
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				processEvidence
				Probe *processEvidence `json:"probe,omitempty"`
			}{currentEvidence(), probe})
		})
	}
	if !*noHealthz {
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	}
	// /livez is a separate liveness endpoint so a release can pass its
	// startup /healthz gate and still fail its liveness probe afterwards.
	mux.HandleFunc("/livez", func(w http.ResponseWriter, _ *http.Request) {
		if *failLivez {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	// A bad release that still passes the platform's post-readiness smoke
	// (which probes / and the health path) and fails real API traffic.
	mux.HandleFunc("/api", func(w http.ResponseWriter, _ *http.Request) {
		if *failAPI {
			http.Error(w, "fixture bad release", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(body)
	})
	log.Printf("hello-server: listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

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
//	(default)      serve the contents of -body-file on / and 200 on /healthz,
//	               echo POST /echo request bodies back;
//	               bind $PORT when -addr is omitted (falling back to 8080)
//	-spin          also burn one CPU forever (cpu-fairness fixture)
//	-ignore-term   ignore SIGTERM (wedged-process fixture)
//	-no-listen     never bind the port, so liveness sees conn_refused
//	-no-healthz    omit /healthz so TCP readiness is the only boot contract
//	-durable-counter serve the pure durable entity counter protocol
//	-echo-session  interactive app-task fixture: report tty and size, echo lines
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
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
	queueAck := flag.Bool("queue-ack", false, "acknowledge push-queue fixture deliveries without returning customer data")
	workerMarker := flag.Bool("worker-startup-marker", false, "record ordinary worker startup for native task isolation checks")
	workerIsolation := flag.Bool("assert-worker-task-isolation", false, "require a fresh task guest without a worker entrypoint or queue listener")
	gitOpsResilience := flag.Bool("gitops-resilience", false, "allow bounded native acceptance request and queue delays")
	durableCounter := flag.Bool("durable-counter", false, "serve pure durable entity transitions")
	echoSession := flag.Bool("echo-session", false, "run the interactive app-task fixture instead of serving")
	identityProbe := flag.Bool("identity-probe", false, "serve /identity, which fetches a workload identity token from guest-init")
	flag.Parse()
	if *workerIsolation {
		if err := assertWorkerTaskIsolation(workerStartupMarker, "127.0.0.1:8080"); err != nil {
			log.Fatal(err)
		}
		fmt.Println(`{"worker_entrypoint_started":false,"worker_listener_started":false}`)
		return
	}
	if *workerMarker {
		if err := os.WriteFile(workerStartupMarker, []byte("started\n"), 0o600); err != nil {
			log.Fatal(err)
		}
	}
	if *echoSession {
		runEchoSession()
		return
	}
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
	registerTCPFixture(mux)
	if *identityProbe {
		serveIdentityProbe(mux)
	}
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
	if *queueAck {
		mux.HandleFunc("/_triggers/esm/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if *gitOpsResilience && !waitGitOpsQueueDelay(w, r) {
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
	// /echo answers with the request body, so service-proxy tests can check
	// that a replayed request still carried it.
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		got, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(got)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if *gitOpsResilience && !waitGitOpsRequestDelay(w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(body)
	})
	log.Printf("hello-server: listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

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
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	addr := flag.String("addr", "", "listen address (defaults to $PORT or :8080)")
	bodyFile := flag.String("body-file", "/app/hello.txt", "file whose contents are served on /")
	spin := flag.Bool("spin", false, "burn one CPU forever")
	ignoreTerm := flag.Bool("ignore-term", false, "ignore SIGTERM")
	noListen := flag.Bool("no-listen", false, "never bind the port")
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
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(body)
	})
	log.Printf("hello-server: listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

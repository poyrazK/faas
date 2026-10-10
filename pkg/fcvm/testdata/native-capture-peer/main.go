// A static, chrooted Unix API peer for native capture protocol acceptance.
// This models Firecracker effects; it is not a microVM or restore smoke test.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

func main() {
	capture := flag.String("capture", "", "original capture UUID")
	drive := flag.String("drive", "", "original private drive binding")
	lost := flag.String("lost", "", "effect whose response is lost")
	flag.Parse()
	if *capture == "" || strings.ContainsAny(*capture, "/\\") || *drive == "" || *drive == "." || *drive == ".." || strings.ContainsAny(*drive, "/\\") {
		os.Exit(2)
	}
	listener, err := net.Listen("unix", "/api.sock")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	paused := false
	var mu sync.Mutex
	server := &http.Server{ReadHeaderTimeout: time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		defer r.Body.Close()
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		action := ""
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/vm" && body["state"] == "Paused" && !paused:
			paused, action = true, "pause"
		case r.Method == http.MethodPatch && r.URL.Path == "/vm" && body["state"] == "Resumed" && paused:
			paused, action = false, "resume"
			if err := writeOriginal(*drive, "changed-private-drive!"); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		case r.Method == http.MethodPut && r.URL.Path == "/snapshot/create" && paused && body["snapshot_type"] == "Full":
			action = "create"
			for _, output := range []struct{ field, kind string }{{"mem_file_path", "mem"}, {"snapshot_path", "vmstate"}} {
				name := "capture-" + *capture + "-" + output.kind
				if body[output.field] != name {
					http.Error(w, "foreign capture output", http.StatusBadRequest)
					return
				}
				if err := writeOriginal(name, "original-"+output.kind); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
		default:
			http.Error(w, "unexpected or repeated effect", http.StatusConflict)
			return
		}
		fmt.Fprintln(os.Stdout, action)
		if action == *lost {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	ready := os.NewFile(3, "original-ready-pipe")
	if _, err := ready.Write([]byte{1}); err != nil {
		os.Exit(1)
	}
	_ = ready.Close()
	if err := server.Serve(listener); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func writeOriginal(name, body string) error {
	// Snapshot outputs must already be original bindings. A missing file
	// must fail instead of silently creating a new unowned jail file.
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, err = file.Write([]byte(body))
	if err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

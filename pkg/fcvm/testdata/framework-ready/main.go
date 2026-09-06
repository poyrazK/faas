package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// Metal-only workload: signal readiness after an actual /signal invocation.
// /healthz alone never marks the framework ready.
func main() {
	var once sync.Once
	var signalErr error
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	http.HandleFunc("/signal", func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() {
			c, err := net.DialTimeout("unix", "/run/guest-init/framework-ready.sock", time.Second)
			if err != nil {
				signalErr = err
				return
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(time.Second))
			_, _ = fmt.Fprint(c, "go124 123\n")
			reply, err := bufio.NewReader(c).ReadString('\n')
			if err != nil || reply != "ok\n" {
				signalErr = fmt.Errorf("framework-ready proxy reply=%q: %v", reply, err)
			}
		})
		if signalErr != nil {
			http.Error(w, signalErr.Error(), 500)
			return
		}
		_, _ = fmt.Fprint(w, "ok")
	})
	http.HandleFunc("/etc/faas/uuid.txt", func(w http.ResponseWriter, _ *http.Request) {
		b, err := os.ReadFile("/etc/faas/uuid.txt")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_, _ = w.Write(b)
	})
	if err := http.ListenAndServe(":8080", nil); err != nil {
		panic(err)
	}
}

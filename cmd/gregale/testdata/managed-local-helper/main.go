//go:build unix

// A small foreground-process fixture. Keep it independent from the large CLI
// test binary so readiness and cancellation budgets measure the supervisor,
// not unrelated test-package initialization and page faults.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(9) }
func main() {
	if len(os.Args) > 1 && os.Args[1] == "fixture" {
		runFixture()
	} else {
		runApp()
	}
}
func runApp() {
	mode := os.Getenv("GREGALE_LOCAL_APP_HELPER")
	if mode == "" {
		return
	}
	fail := func(err error) {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(9)
	}
	_, _ = fmt.Fprintln(os.Stdout, "app-private-log")
	if mode == "exit" {
		os.Exit(7)
	}
	if mode == "exit-zero" {
		os.Exit(0)
	}
	if mode == "child" {
		signal.Ignore(syscall.SIGTERM)
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			fail(err)
		}
		if err := os.WriteFile("child-address", []byte(listener.Addr().String()), 0o600); err != nil {
			fail(err)
		}
		_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
		os.Exit(0)
	}
	host, port := os.Getenv("HOST"), os.Getenv("PORT")
	baseURL := "http://" + net.JoinHostPort(host, port)
	if host != "127.0.0.1" || os.Getenv("GREGALE_TEST_HOST") != host || os.Getenv("GREGALE_TEST_PORT") != port || os.Getenv("GREGALE_TEST_URL") != baseURL || os.Getenv("FAAS_TOKEN") != "" {
		fail(fmt.Errorf("managed app received invalid environment"))
	}
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[len(os.Args)-1], "endpoint=") && os.Args[len(os.Args)-1] != "endpoint="+net.JoinHostPort(host, port) {
		fail(fmt.Errorf("managed app received invalid command placeholders"))
	}
	if mode == "descendant" {
		exe, err := os.Executable()
		if err != nil {
			fail(err)
		}
		child := exec.Command(exe, "app")
		child.Env = append(os.Environ(), "GREGALE_LOCAL_APP_HELPER=child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			fail(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := os.Stat("child-address"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				fail(fmt.Errorf("child did not start"))
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(host, port))
	if err != nil {
		fail(err)
	}
	marker, _ := json.Marshal(map[string]string{"url": baseURL, "run": os.Getenv("GREGALE_TEST_RUN_ID"), "case": os.Getenv("GREGALE_TEST_CASE")})
	if err := appendLocalAppHelperFile("starts.jsonl", append(marker, '\n')); err != nil {
		fail(err)
	}
	started := time.Now()
	var mu sync.Mutex
	server := &http.Server{ReadHeaderTimeout: time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/health":
			if mode == "never-ready" || time.Since(started) < 120*time.Millisecond {
				w.WriteHeader(503)
				return
			}
			w.WriteHeader(204)
		case "/redirect":
			http.Redirect(w, request, "/health", http.StatusFound)
		case "/check":
			var data map[string]any
			_ = json.Unmarshal([]byte(os.Getenv("GREGALE_TEST_DATA_JSON")), &data)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"run": os.Getenv("GREGALE_TEST_RUN_ID"), "customer": data["customer"]})
		case "/crash":
			os.Exit(7)
		case "/slow":
			select {
			case <-request.Context().Done():
			case <-time.After(10 * time.Second):
			}
		default:
			mu.Lock()
			err := appendLocalAppHelperFile("order.log", []byte(strings.TrimPrefix(request.URL.Path, "/")+":"+os.Getenv("GREGALE_TEST_CASE")+"\n"))
			mu.Unlock()
			if err != nil {
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(204)
		}
	})
	if mode == "ignore-term" {
		signal.Ignore(syscall.SIGTERM)
	} else {
		terminated := make(chan os.Signal, 1)
		signal.Notify(terminated, syscall.SIGTERM)
		go func() {
			<-terminated
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
		}()
	}
	_ = server.Serve(listener)
	os.Exit(0)
}

func appendLocalAppHelperFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = file.Write(data)
	return err
}

func runFixture() {
	if os.Getenv("GREGALE_LOCAL_APP_HELPER") == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", os.Getenv("GREGALE_TEST_URL")+"/"+os.Args[len(os.Args)-1], nil)
	if err != nil {
		fail(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		fail(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 204 {
		fail(fmt.Errorf("fixture status = %d", response.StatusCode))
	}
}

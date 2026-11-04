package main

import (
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
)

// registerOOMFixture (ADR-121 amendment) lets a metal test make the guest
// kernel kill this single-process app for memory. POST /__fixture/oom
// exhausts memory while serving; FIXTURE_OOM_AT_START=1 does it before the
// server listens, so the kill happens during startup.
func registerOOMFixture(mux *http.ServeMux) {
	if os.Getenv("FIXTURE_OOM_AT_START") == "1" {
		exhaustMemory()
	}
	mux.HandleFunc("POST /__fixture/oom", func(w http.ResponseWriter, _ *http.Request) {
		exhaustMemory()
		http.Error(w, "memory was not exhausted", http.StatusInternalServerError)
	})
}

// exhaustMemory retains and touches 1 MiB chunks, up to 8 GiB, with the
// collector off, so the kernel's OOM killer ends the process first.
func exhaustMemory() {
	debug.SetGCPercent(-1)
	chunks := make([][]byte, 0, 8<<10)
	for i := 0; i < 8<<10; i++ {
		chunk := make([]byte, 1<<20)
		for page := 0; page < len(chunk); page += 4096 {
			chunk[page] = byte(i)
		}
		chunks = append(chunks, chunk)
	}
	runtime.KeepAlive(chunks)
}

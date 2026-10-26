// Command memapp is the app of the metal density tests
// (TestMetalMeasuredAdmissionDensity and friends): it serves :8080 and grows
// or releases its resident memory on request. Build it static for the
// layer's /upper/app/memapp with app.json {"entrypoint":["/app/memapp"],"port":8080}.
package main

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync"
)

const pageSize = 4096

var (
	mu   sync.Mutex
	held [][]byte
)

// grow allocates mb MiB and writes every page, so the guest backs it.
func grow(mb int) {
	b := make([]byte, mb<<20)
	for i := 0; i < len(b); i += pageSize {
		b[i] = 1
	}
	mu.Lock()
	held = append(held, b)
	mu.Unlock()
}

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	// /heavy stands in for a framework's warm working set.
	http.HandleFunc("/heavy", func(w http.ResponseWriter, _ *http.Request) {
		grow(96)
		fmt.Fprintln(w, "heavy")
	})
	http.HandleFunc("/alloc", func(w http.ResponseWriter, r *http.Request) {
		mb, err := strconv.Atoi(r.URL.Query().Get("mb"))
		if err != nil || mb <= 0 || mb > 900 {
			http.Error(w, "mb must be 1-900", http.StatusBadRequest)
			return
		}
		grow(mb)
		fmt.Fprintln(w, "allocated", mb)
	})
	http.HandleFunc("/free", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		held = nil
		mu.Unlock()
		debug.FreeOSMemory()
		fmt.Fprintln(w, "freed")
	})
	if err := http.ListenAndServe(":8080", nil); err != nil {
		panic(err)
	}
}

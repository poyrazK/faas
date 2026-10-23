// server is the metal fixture for the Go SDK's tracing package (ADR-958
// amendment): an HTTP app instrumented only with tracing.Start, Handler,
// Transport and Open, and no OpenTelemetry dependency. Each request runs one
// database query against an in-process driver and one HTTP call to a loopback
// listener, so the debugger evidence must show a postgresql and an http
// app_dependency.
package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/poyrazK/faas/sdk/go/tracing"
)

func main() {
	shutdown := tracing.Start()
	defer func() { _ = shutdown(context.Background()) }()

	sql.Register("fixture", fixtureDriver{})
	db, err := tracing.Open("fixture", "", "postgresql")
	if err != nil {
		log.Fatalf("go-tracing-helper: open db: %v", err)
	}
	stock, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("go-tracing-helper: stock listener: %v", err)
	}
	go func() {
		_ = http.Serve(stock, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "in stock\n") }))
	}()
	client := &http.Client{Transport: tracing.Transport(nil)}

	mux := http.NewServeMux()
	// Both patterns name the method: ServeMux panics on "GET /" beside a
	// method-less "/healthz" (neither is more specific).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.QueryContext(r.Context(), "SELECT id FROM orders WHERE id = $1", 42)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = rows.Close()
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://"+stock.Addr().String()+"/stock", nil)
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		_ = resp.Body.Close()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "helper "+os.Getenv("FAAS_TRACING_ENABLED")+"\n")
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("go-tracing-helper: listening on :%s (tracing %v)", port, tracing.Enabled())
	log.Fatal(http.ListenAndServe(":"+port, tracing.Handler(mux)))
}

// fixtureDriver answers every query with no rows.
type fixtureDriver struct{}

func (fixtureDriver) Open(string) (driver.Conn, error) { return fixtureConn{}, nil }

type fixtureConn struct{}

func (fixtureConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (fixtureConn) Close() error                        { return nil }
func (fixtureConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (fixtureConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return fixtureRows{}, nil
}

type fixtureRows struct{}

func (fixtureRows) Columns() []string         { return []string{"id"} }
func (fixtureRows) Close() error              { return nil }
func (fixtureRows) Next([]driver.Value) error { return io.EOF }

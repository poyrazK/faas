// commitconsumer is the HTTP application deployed into the native Commit VM.
package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type event struct {
	Source string `json:"source"`
	ID     string `json:"id"`
}

func main() {
	cfg, err := pgxpool.ParseConfig(os.Getenv("COMMIT_CONSUMER_DATABASE_URL"))
	if err != nil || cfg.ConnConfig.TLSConfig == nil || cfg.ConnConfig.TLSConfig.InsecureSkipVerify {
		log.Fatal("consumer requires a verified TLS database configuration")
	}
	ca, err := os.ReadFile("/consumer-ca.pem")
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(ca) {
		log.Fatal("consumer could not load the fixture CA")
	}
	cfg.ConnConfig.TLSConfig.RootCAs = roots
	// The dedicated TCP proxy forwards to the fixture certificate for localhost.
	cfg.ConnConfig.TLSConfig.ServerName = "localhost"
	db, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		log.Fatal("consumer could not configure the database pool")
	}
	defer db.Close()
	processID := uuid.NewString()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /{$}", func(w http.ResponseWriter, r *http.Request) {
		var e event
		operation := r.Header.Get("X-Gregale-Operation-Id")
		generation, err := strconv.ParseInt(r.Header.Get("X-Gregale-Operation-Generation"), 10, 64)
		if err != nil || generation < 1 || uuid.Validate(operation) != nil ||
			json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&e) != nil ||
			e.Source == "" || uuid.Validate(e.ID) != nil {
			http.Error(w, "invalid Commit delivery", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		inserted, err := recordDelivery(ctx, db, e, operation, processID, generation)
		if err != nil {
			log.Print(err)
			http.Error(w, "consumer transaction failed", http.StatusServiceUnavailable)
			return
		}
		if inserted {
			// Simulate losing the HTTP acknowledgment after PostgreSQL commits.
			// The guest supervisor must start a new process for the retry.
			os.Exit(73)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"recovered":true}`)
	})
	server := http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}

func recordDelivery(ctx context.Context, db *pgxpool.Pool, e event, operation, processID string, generation int64) (bool, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("open consumer transaction: %T", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	marker, err := tx.Exec(ctx, `INSERT INTO processed_events(source, event_id) VALUES ($1, $2)
		ON CONFLICT (source, event_id) DO NOTHING`, e.Source, e.ID)
	if err != nil {
		return false, fmt.Errorf("record consumer deduplication marker: %T", err)
	}
	inserted := marker.RowsAffected() == 1
	if inserted {
		if _, err := tx.Exec(ctx, `UPDATE business_effects SET total = total + 1`); err != nil {
			return false, fmt.Errorf("write consumer effect: %T", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO deliveries(source, event_id, operation_id, generation, process_id, applied)
		VALUES ($1, $2, $3, $4, $5, $6)`, e.Source, e.ID, operation, generation, processID, inserted); err != nil {
		return false, fmt.Errorf("record consumer delivery: %T", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit consumer transaction: %T", err)
	}
	return inserted, nil
}

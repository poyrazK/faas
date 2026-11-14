package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaskDatabasePoolBounds(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://localhost/tasks")
	if err != nil {
		t.Fatal(err)
	}
	configureTaskDatabasePool(cfg, 16)
	if cfg.MaxConns != 20 || cfg.ConnConfig.ConnectTimeout != 5*time.Second {
		t.Fatal("pool does not cover workers and reserved operations with a dial bound")
	}
	tracer := taskDatabaseTracer{}
	acquire := tracer.TraceAcquireStart(context.Background(), nil, pgxpool.TraceAcquireStartData{})
	deadline, ok := acquire.Deadline()
	if !ok || time.Until(deadline) > 5*time.Second {
		t.Fatal("unbounded pool acquisition")
	}
	tracer.TraceAcquireEnd(acquire, nil, pgxpool.TraceAcquireEndData{})
	if acquire.Err() == nil {
		t.Fatal("acquisition leaked its timer")
	}
	query := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{})
	if deadline, ok := query.Deadline(); !ok || time.Until(deadline) > 10*time.Second {
		t.Fatal("unbounded query")
	}
	tracer.TraceQueryEnd(query, nil, pgx.TraceQueryEndData{})
	if query.Err() == nil {
		t.Fatal("query leaked its timer")
	}
	parent, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	query = tracer.TraceQueryStart(parent, nil, pgx.TraceQueryStartData{})
	want, _ := parent.Deadline()
	got, _ := query.Deadline()
	tracer.TraceQueryEnd(query, nil, pgx.TraceQueryEndData{})
	if got != want || parent.Err() != nil {
		t.Fatal("query changed an explicit deadline or canceled its parent")
	}
}

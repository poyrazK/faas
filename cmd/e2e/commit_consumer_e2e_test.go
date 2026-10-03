package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The producer and consumer own different business databases. Real API,
// scheduler and gateway processes deliver to a real HTTP child process through
// the VMMD protocol fixture; the native gate separately proves VM execution.
func TestE2E_CommitHTTPConsumerCrashRecovery(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL required")
	}
	consumer := pgtest.OpenDatabase(t)
	ctx := t.Context()
	if _, err := consumer.Exec(ctx, `CREATE TABLE processed_events(consumer text NOT NULL,source text NOT NULL,event_id uuid NOT NULL,PRIMARY KEY(consumer,source,event_id)); CREATE TABLE business_effects(id integer PRIMARY KEY,total integer NOT NULL); INSERT INTO business_effects VALUES(1,0)`); err != nil {
		t.Fatal(err)
	}
	f := newNormalPathFixtureWithPlanAndEnv(t, "commit-consumer", api.PlanPro, commitOperationEnvironment(t, "FAAS_COMMIT_API_ENABLED=true")...)
	if f == nil {
		t.Fatal("Commit consumer acceptance requires PostgreSQL")
	}
	body, code := doReq(t, f.h, f.key, http.MethodPatch, "/v1/apps/commit-consumer", map[string]any{
		"require_authn": false,
		"public_auth":   map[string]any{"mode": "open"},
		"retry_policy":  map[string]any{"max_attempts": 5, "base_seconds": 1, "max_seconds": 1, "jitter_seconds": 0},
	})
	if code != http.StatusOK {
		t.Fatalf("consumer retry policy: %d %s", code, body)
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "consumer")
	f.vmmd.SetVersion(instance.ID, "consumer")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:consumer\n", 10*time.Second)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	consumerCtx, cancelConsumer := context.WithCancel(ctx)
	t.Cleanup(cancelConsumer)
	tmp := t.TempDir()
	beforeFile, afterFile := filepath.Join(tmp, "before-commit"), filepath.Join(tmp, "after-commit")
	first, err := startCommitHTTPConsumer(consumerCtx, consumer.Config().ConnString(), consumer.Config().ConnConfig.Database, address, "before_commit", beforeFile)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	f.vmmd.SetForwardHandler(instance.ID, func(ctx context.Context, request e2etest.RequestCapture) (e2etest.FakeResponse, error) {
		req, err := http.NewRequestWithContext(ctx, request.Init.Method, "http://"+address+request.Init.RequestUri, bytes.NewReader(request.Body))
		if err != nil {
			return e2etest.FakeResponse{}, err
		}
		for _, header := range request.Init.Headers {
			req.Header.Add(header.Name, header.Value)
		}
		response, err := client.Do(req)
		if err != nil {
			return e2etest.FakeResponse{}, status.Error(codes.Unavailable, "consumer connection closed before response")
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		return e2etest.FakeResponse{Status: response.StatusCode, Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: response.Header.Get("Content-Type")}}, Body: body}, err
	})
	recovered := make(chan error, 1)
	go func() {
		if err := awaitCommitConsumerCrash(first, 91); err != nil {
			recovered <- err
			return
		}
		if err := commitConsumerCounts(consumerCtx, consumer, 0, 0); err != nil {
			recovered <- fmt.Errorf("consumer crash before commit: %w", err)
			return
		}
		second, err := startCommitHTTPConsumer(consumerCtx, consumer.Config().ConnString(), consumer.Config().ConnConfig.Database, address, "after_commit", afterFile)
		if err == nil {
			err = awaitCommitConsumerCrash(second, 92)
		}
		if err != nil {
			recovered <- err
			return
		}
		if err := commitConsumerCounts(consumerCtx, consumer, 1, 1); err != nil {
			recovered <- fmt.Errorf("consumer crash after commit: %w", err)
			return
		}
		_, err = startCommitHTTPConsumer(consumerCtx, consumer.Config().ConnString(), consumer.Config().ConnConfig.Database, address, "healthy", "")
		recovered <- err
	}()
	var repairPoison func(context.Context, string) error
	completed := runCommitProducerHandoff(t, f.h, f.store, f.key, f.app.ID, "consumer-orders", commitHandoffOptions{SourceRecovery: true, RelayFaults: true, RepairPoison: &repairPoison})
	select {
	case err := <-recovered:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("consumer recovery coordinator did not finish")
	}
	if completed.Attempts < 3 {
		t.Fatalf("consumer crashes did not cause durable retries: attempts=%d", completed.Attempts)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(completed.Result, &result); err != nil {
		t.Fatalf("decode completed consumer result: %v", err)
	}
	if commitOperationInstance(t, completed) != instance.ID || !result.OK {
		t.Fatalf("recovered HTTP consumer did not complete the operation: incarnation=%s result=%s", completed.IncarnationID, completed.Result)
	}
	before, err := os.ReadFile(beforeFile)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(afterFile)
	if err != nil || string(before) != string(after) {
		t.Fatalf("consumer retry changed source/event identity: before=%q after=%q err=%v", before, after, err)
	}
	var invocation api.InvokeRequest
	if err := json.Unmarshal(completed.Request, &invocation); err != nil {
		t.Fatal(err)
	}
	var delivered events.Envelope
	if err := json.Unmarshal(invocation.Payload, &delivered); err != nil || string(before) != delivered.Source+"\n"+delivered.ID+"\n" {
		t.Fatalf("consumer did not receive the durable envelope identity: %+v err=%v", delivered, err)
	}
	// Concurrent duplicate HTTP deliveries reach the recovered business
	// consumer. Its durable marker must suppress effects after process restart.
	failures := make(chan error, 8)
	var requests sync.WaitGroup
	for range 8 {
		requests.Add(1)
		go func() {
			defer requests.Done()
			response, err := client.Post("http://"+address+"/", "application/cloudevents+json", bytes.NewReader(invocation.Payload))
			if err == nil {
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode != http.StatusOK {
					err = fmt.Errorf("duplicate consumer status: %d", response.StatusCode)
				}
			}
			failures <- err
		}()
	}
	requests.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := commitConsumerCounts(ctx, consumer, 1, 1); err != nil {
		t.Fatal(err)
	}
	// The poison event did not stop the healthy event or its retries. Repair
	// and replay it while the two managed relays still compete, then verify
	// exactly one additional business effect for that distinct event.
	sourceID := strings.TrimPrefix(delivered.Source, "gregale.commit.")
	blockedBody, blockedCode := doReq(t, f.h, f.key, http.MethodGet, "/v1/commit-sources/"+sourceID+"/blocked-events", nil)
	var blocked api.CommitBlockedEventsResponse
	if err := json.Unmarshal(blockedBody, &blocked); err != nil || blockedCode != http.StatusOK || len(blocked.Items) != 1 {
		t.Fatalf("poison event visibility: %d %s %v", blockedCode, blockedBody, err)
	}
	// The producer handoff owns this source database; repairs are explicitly
	// authorized fixture operations, never relay-side payload mutations.
	if repairPoison == nil {
		t.Fatal("poison owner repair callback missing")
	}
	if err := repairPoison(ctx, blocked.Items[0].EventID); err != nil {
		t.Fatal(err)
	}
	replayBody, replayCode := doReq(t, f.h, f.key, http.MethodPost, "/v1/commit-sources/"+sourceID+"/events/"+blocked.Items[0].EventID+"/replay", nil)
	if replayCode != http.StatusAccepted {
		t.Fatalf("poison replay: %d %s", replayCode, replayBody)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		receipt, err := f.store.CommitReceiptByEvent(ctx, completed.AccountID, sourceID, blocked.Items[0].EventID)
		if err == nil {
			waitCommitOperationCompleted(t, f.h, f.store, f.key, completed.AccountID, receipt.OperationID, 30*time.Second)
			break
		}
		if !errors.Is(err, state.ErrNotFound) || time.Now().After(deadline) {
			t.Fatalf("poison replay acceptance: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := commitConsumerCounts(ctx, consumer, 2, 2); err != nil {
		t.Fatal(err)
	}
	t.Logf("producer death, relay death before checkpoint, competing relays, source outage, credential rotation, consumer crashes, concurrent duplicates and poison repair preserved one business effect per event; first event attempts=%d", completed.Attempts)
}

type commitHTTPConsumerProcess struct{ exit <-chan error }

func startCommitHTTPConsumer(ctx context.Context, databaseURL, databaseName, address, mode, barrier string) (*commitHTTPConsumerProcess, error) {
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCommitHTTPConsumerProcess$")
	command.Env = append(os.Environ(), "GREGALE_COMMIT_HTTP_CONSUMER=1", "DATABASE_URL="+databaseURL, "GREGALE_COMMIT_CONSUMER_DATABASE="+databaseName, "GREGALE_COMMIT_CONSUMER_ADDR="+address, "GREGALE_COMMIT_CONSUMER_MODE="+mode, "GREGALE_COMMIT_CONSUMER_BARRIER="+barrier)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- ""
		}
		for scanner.Scan() {
		}
	}()
	exit := make(chan error, 1)
	go func() { exit <- command.Wait() }()
	select {
	case line := <-ready:
		if line == "listening" {
			return &commitHTTPConsumerProcess{exit: exit}, nil
		}
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
	}
	_ = command.Process.Kill()
	<-exit
	return nil, errors.New("HTTP consumer did not become ready")
}

func awaitCommitConsumerCrash(process *commitHTTPConsumerProcess, code int) error {
	err := <-process.exit
	var exit *exec.ExitError
	if err == nil {
		return fmt.Errorf("consumer crash: wanted exit %d, process completed successfully", code)
	}
	if !errors.As(err, &exit) || exit.ExitCode() != code {
		return fmt.Errorf("consumer crash: wanted exit %d: %w", code, err)
	}
	return nil
}

func commitConsumerCounts(ctx context.Context, pool *pgxpool.Pool, effects, markers int) error {
	var actualEffects, actualMarkers int
	err := pool.QueryRow(ctx, `SELECT (SELECT total FROM business_effects WHERE id=1),(SELECT count(*) FROM processed_events)`).Scan(&actualEffects, &actualMarkers)
	if err != nil {
		return fmt.Errorf("read consumer effects: %w", err)
	}
	if effects != actualEffects || markers != actualMarkers {
		return fmt.Errorf("effects=%d markers=%d, wanted %d/%d", actualEffects, actualMarkers, effects, markers)
	}
	return nil
}

func TestCommitHTTPConsumerProcess(t *testing.T) {
	if os.Getenv("GREGALE_COMMIT_HTTP_CONSUMER") != "1" {
		return
	}
	config, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	// ConnString retains the original DSN after OpenDatabase changes its
	// parsed configuration. Carry the private database explicitly to children.
	config.ConnConfig.Database = os.Getenv("GREGALE_COMMIT_CONSUMER_DATABASE")
	config.ConnConfig.RuntimeParams["search_path"] = "public"
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var schemaReady bool
	if err := pool.QueryRow(t.Context(), `SELECT to_regclass('public.processed_events') IS NOT NULL AND to_regclass('public.business_effects') IS NOT NULL`).Scan(&schemaReady); err != nil || !schemaReady {
		t.Fatalf("private consumer database schema unavailable: ready=%t err=%v", schemaReady, err)
	}
	listener, err := net.Listen("tcp", os.Getenv("GREGALE_COMMIT_CONSUMER_ADDR"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: commitHTTPConsumerHandler(pool, os.Getenv("GREGALE_COMMIT_CONSUMER_MODE"), os.Getenv("GREGALE_COMMIT_CONSUMER_BARRIER")), ReadHeaderTimeout: 5 * time.Second}
	fmt.Println("listening")
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
}

func commitHTTPConsumerHandler(pool *pgxpool.Pool, mode, barrier string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event events.Envelope
		if r.Method != http.MethodPost || json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&event) != nil || event.ID == "" || !strings.HasPrefix(event.Source, "gregale.commit.") || event.Type != "order.created" {
			http.Error(w, "invalid event", http.StatusBadRequest)
			return
		}
		tx, err := pool.Begin(r.Context())
		if err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		defer func(parent context.Context) {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(cleanupCtx)
		}(r.Context())
		inserted, err := tx.Exec(r.Context(), `INSERT INTO processed_events(consumer,source,event_id) VALUES('order-accounting',$1,$2::uuid) ON CONFLICT DO NOTHING`, event.Source, event.ID)
		if err == nil && inserted.RowsAffected() == 1 {
			_, err = tx.Exec(r.Context(), `UPDATE business_effects SET total=total+1 WHERE id=1`)
		}
		if err == nil && mode == "before_commit" {
			if err := os.WriteFile(barrier, []byte(event.Source+"\n"+event.ID+"\n"), 0600); err != nil {
				http.Error(w, "barrier unavailable", http.StatusServiceUnavailable)
				return
			}
			os.Exit(91)
		}
		if err == nil {
			err = tx.Commit(r.Context())
		}
		if err != nil {
			http.Error(w, "transaction failed", http.StatusServiceUnavailable)
			return
		}
		if mode == "after_commit" {
			if err := os.WriteFile(barrier, []byte(event.Source+"\n"+event.ID+"\n"), 0600); err != nil {
				http.Error(w, "barrier unavailable", http.StatusServiceUnavailable)
				return
			}
			os.Exit(92)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})
}

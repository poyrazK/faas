// adr: 1351
package outbound

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/state"
)

func rawRecorderSeal(t *testing.T, f *simulationFixture) api.ScenarioActionSnapshot {
	t.Helper()
	s, err := f.store.(state.ScenarioActionSnapshotStore).SealScenarioTestActions(t.Context(), f.req.AccountID, f.req.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRehearsalRecorderRequiresOwnerAcknowledgementAndDurableBrokerReceipt(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, mode := range []string{"healthy-empty", "broker-never-admitted", "owner-lost", "missing-recorder"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				f := newSimulationFixtureWithRecorder(t, backend, mode != "missing-recorder")
				store := f.store.(rehearsalrecorder.Store)
				if mode == "broker-never-admitted" {
					_ = f.requestID(t)
				}
				if mode != "missing-recorder" {
					status, err := store.SettleScenarioRecorder(t.Context(), f.recorderIdentity, f.recorderID, false, true)
					if err != nil || status != "recording" {
						t.Fatalf("acknowledged without closing fence: %q %v", status, err)
					}
				}
				s := rawRecorderSeal(t, f)
				if mode == "missing-recorder" {
					if s.Status != "incomplete" || s.IncompleteRecorderSessions == 0 {
						t.Fatalf("missing owner passed: %+v", s)
					}
					return
				}
				if s.Status != "pending" || s.PendingRecorderSessions != 1 || len(s.SimulatedAttempts) != 0 {
					t.Fatalf("empty inventory hid recorder gap: %+v", s)
				}
				if _, err := store.BeginScenarioRecorderRequest(t.Context(), f.recorderIdentity, f.recorderID); !errors.Is(err, rehearsalrecorder.ErrClosing) {
					t.Fatalf("closing accepted a request: %v", err)
				}
				if _, err := store.StartScenarioRecorder(t.Context(), f.recorderIdentity); err == nil {
					t.Fatal("replacement adopted the owner session")
				}
				if mode == "owner-lost" {
					// Absence of activity is deliberately not an owner acknowledgement.
					if again := rawRecorderSeal(t, f); again.Status != "pending" {
						t.Fatalf("inactivity became completion: %+v", again)
					}
					return
				}
				status, err := store.SettleScenarioRecorder(t.Context(), f.recorderIdentity, f.recorderID, false, true)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "broker-never-admitted" {
					if status != "closing" || rawRecorderSeal(t, f).Status != "pending" {
						t.Fatal("broker failure became an empty passing snapshot")
					}
					if c := actioncompare.Compare(simulationEvidence(s), simulationEvidence(s)); c.Status != "inconclusive" {
						t.Fatalf("pending coverage compared: %+v", c)
					}
				} else if status != "closed" || rawRecorderSeal(t, f).Status != "complete" {
					t.Fatal("healthy acknowledged recorder did not close")
				}
			})
		}
	}
}

func TestRehearsalRecorderIdentityAndRequestReceiptCannotBeReused(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newSimulationFixture(t, backend)
			store := f.store.(rehearsalrecorder.Store)
			for _, field := range []string{"account", "app", "run", "instance", "deployment"} {
				identity := f.recorderIdentity
				switch field {
				case "account":
					identity.AccountID = uuid.NewString()
				case "app":
					identity.AppID = uuid.NewString()
				case "run":
					identity.RunID = strings.Repeat("f", 32)
				case "instance":
					identity.InstanceID = uuid.NewString()
				case "deployment":
					identity.DeploymentID = uuid.NewString()
				}
				if _, err := store.SettleScenarioRecorder(t.Context(), identity, f.recorderID, false, true); err == nil {
					t.Fatalf("cross-%s session authority accepted", field)
				}
			}
			req := f.req
			req.RecorderRequestID, req.Integration, req.Method, req.Path, req.Body = f.requestID(t), "imports", "POST", "/imports", []byte(`{}`)
			simulator := f.store.(SimulationStore)
			d, err := simulator.BeginScenarioSimulation(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := simulator.BeginScenarioSimulation(t.Context(), req); err == nil {
				t.Fatal("one obligation admitted multiple attempts")
			}
			forged := req
			forged.RecorderRequestID = uuid.NewString()
			if err := simulator.FinishScenarioSimulation(t.Context(), forged, d.Attempt.ID, "completed"); err == nil {
				t.Fatal("foreign obligation acknowledged attempt")
			}
			forged = req
			forged.InstanceID = uuid.NewString()
			if err := simulator.FinishScenarioSimulation(t.Context(), forged, d.Attempt.ID, "completed"); err == nil {
				t.Fatal("foreign instance acknowledged attempt")
			}
			if err := simulator.FinishScenarioSimulation(t.Context(), req, d.Attempt.ID, "completed"); err != nil {
				t.Fatal(err)
			}
			if s := f.seal(t); s.Status != "complete" || len(s.SimulatedAttempts) != 1 {
				t.Fatalf("failed transaction changed evidence: %+v", s)
			}
		})
	}
}

type recorderFailureStore struct {
	store      rehearsalrecorder.Store
	failBegin  atomic.Bool
	failSettle atomic.Bool
}

func (s *recorderFailureStore) StartScenarioRecorder(ctx context.Context, id rehearsalrecorder.Identity) (string, error) {
	return s.store.StartScenarioRecorder(ctx, id)
}
func (s *recorderFailureStore) BeginScenarioRecorderRequest(ctx context.Context, id rehearsalrecorder.Identity, session string) (string, error) {
	if s.failBegin.Load() {
		return "", errors.New("database unavailable before request persistence")
	}
	return s.store.BeginScenarioRecorderRequest(ctx, id, session)
}
func (s *recorderFailureStore) SettleScenarioRecorder(ctx context.Context, id rehearsalrecorder.Identity, session string, failed, ack bool) (string, error) {
	if s.failSettle.Load() {
		return "", errors.New("recorder receipt storage unavailable")
	}
	return s.store.SettleScenarioRecorder(ctx, id, session, failed, ack)
}

func TestRehearsalRecorderOwnerLossDuringStorageFailureStillJoinsConnections(t *testing.T) {
	f := newSimulationFixtureWithRecorder(t, "memory", false)
	store := &recorderFailureStore{store: f.store.(rehearsalrecorder.Store)}
	session, err := rehearsalrecorder.Start(t.Context(), t.Context(), store, f.recorderIdentity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Stop)
	session.Activate()
	stopped := make(chan struct{})
	var connection *rehearsalrecorder.Connection
	connection, err = session.Attach(func() { connection.Detach(false); close(stopped) })
	if err != nil {
		t.Fatal(err)
	}
	store.failSettle.Store(true)
	session.Stop()
	select {
	case <-session.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("owner loss did not join the recorder")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("database failure leaked an attached relay")
	}
	store.failSettle.Store(false)
	if s := rawRecorderSeal(t, f); s.Status != "pending" || s.PendingRecorderSessions != 1 {
		t.Fatalf("lost owner receipt became complete: %+v", s)
	}
}

func TestRehearsalRecorderConcurrentClosingPreservesEveryAdmittedObligation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newSimulationFixture(t, backend)
			store := f.store.(rehearsalrecorder.Store)
			type result struct {
				id  string
				err error
			}
			results := make(chan result, 8)
			start := make(chan struct{})
			for range 8 {
				go func() {
					<-start
					id, err := store.BeginScenarioRecorderRequest(t.Context(), f.recorderIdentity, f.recorderID)
					results <- result{id, err}
				}()
			}
			close(start)
			if s := rawRecorderSeal(t, f); s.Status != "pending" {
				t.Fatalf("closing skipped owner: %+v", s)
			}
			accepted := 0
			for range 8 {
				r := <-results
				if r.err != nil {
					if !errors.Is(r.err, rehearsalrecorder.ErrClosing) {
						t.Fatal(r.err)
					}
					continue
				}
				accepted++
				req := f.req
				req.RecorderRequestID, req.Integration, req.Method, req.Path, req.Body = r.id, "imports", "POST", "/imports", []byte(`{}`)
				d, err := f.store.(SimulationStore).BeginScenarioSimulation(t.Context(), req)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.store.(SimulationStore).FinishScenarioSimulation(t.Context(), req, d.Attempt.ID, "completed"); err != nil {
					t.Fatal(err)
				}
			}
			if s := f.seal(t); s.Status != "complete" || len(s.SimulatedAttempts) != accepted {
				t.Fatalf("closing lost admitted requests: %+v accepted=%d", s, accepted)
			}
		})
	}
}

func TestRehearsalRecorderFailedWriteRemainsIncompleteAfterStorageRecovers(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newSimulationFixtureWithRecorder(t, backend, false)
			store := &recorderFailureStore{store: f.store.(rehearsalrecorder.Store)}
			session, err := rehearsalrecorder.Start(t.Context(), t.Context(), store, f.recorderIdentity)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(session.Stop)
			session.Activate()
			store.failBegin.Store(true)
			if _, err := session.Begin(t.Context()); err == nil {
				t.Fatal("failed request write accepted")
			}
			store.failBegin.Store(false)
			deadline := time.Now().Add(3 * time.Second)
			for {
				s := rawRecorderSeal(t, f)
				if s.Status == "incomplete" {
					if s.IncompleteRecorderSessions == 0 || len(s.SimulatedAttempts) != 0 {
						t.Fatalf("failed recording was concealed: %+v", s)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("failed recorder never settled: %+v", s)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestRehearsalRecorderSQLFencesRetainIdentityAndTerminalReceipt(t *testing.T) {
	f := newSimulationFixture(t, "postgres")
	for _, query := range []string{
		`UPDATE scenario_recorder_sessions SET deployment_id=gen_random_uuid() WHERE id=$1`,
		`UPDATE scenario_recorder_sessions SET instance_id=gen_random_uuid() WHERE id=$1`,
	} {
		_, err := f.pool.Exec(t.Context(), query, f.recorderID)
		assertRecorderSQLFence(t, err)
	}
	if s := f.seal(t); s.Status != "complete" {
		t.Fatalf("healthy recorder: %+v", s)
	}
	_, err := f.pool.Exec(t.Context(), `UPDATE scenario_recorder_sessions SET status='recording' WHERE id=$1`, f.recorderID)
	assertRecorderSQLFence(t, err)
	_, err = f.pool.Exec(t.Context(), `UPDATE scenario_test_action_policies SET recorder_closing=false WHERE app_id=$1`, f.req.AppID)
	assertRecorderSQLFence(t, err)
}

func assertRecorderSQLFence(t *testing.T, err error) {
	t.Helper()
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23514" || !strings.Contains(pgerr.Message, "recorder") {
		t.Fatalf("recorder fence did not reject mutation: %v", err)
	}
}

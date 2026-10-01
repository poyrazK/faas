//go:build !no_pg

package state

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"strings"
	"testing"
	"time"
)

func TestPgInstanceApplicationStandardPromotion(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardPromotionLifecycle(t, s)
}
func TestPgInstanceApplicationStandardPromotionRestart(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardPromotionRestart(t, s)
}

func TestPgInstanceApplicationStandardPromotionAfterInitialGrantExpiry(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	f := newRuntimeCaptureFixture(t, s, true)
	ins, err := s.CreateInstance(t.Context(), f.app.ID, f.dep.ID, string(StateWaking), 128, f.nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	b := runtimeCaptureTestBinding(t, s, ins)
	b.ExpiresAtUnixNano = time.Now().Add(300 * time.Millisecond).UnixNano()
	raw, _ := json.Marshal(b)
	if _, err := pool.Exec(t.Context(), `INSERT INTO instance_application_standard_boots(token,instance_id,expected_state,binding) VALUES($1,$2,$3,$4::jsonb)`, b.Token, ins.ID, ins.State, raw); err != nil {
		t.Fatal(err)
	}
	parent := runtimeadmission.Receipt{Binding: b, NativeInputHash: strings.Repeat("b", 64), Netns: "historical-paused", HostIP: "10.100.0.8", LeaseUID: 20008, Paused: true, CompletedAtUnixNano: time.Now().UnixNano()}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateWarm, parent); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(time.Unix(0, b.ExpiresAtUnixNano)) + time.Millisecond)
	loaded, err := s.GetInstanceApplicationStandardWarmParent(t.Context(), ins.ID)
	if err != nil || loaded != parent {
		t.Fatalf("healthy paused identity expired with boot authority: %+v %v", loaded, err)
	}
	p, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent))
	if err != nil {
		t.Fatal(err)
	}
	r := parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	actual, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r)
	if err != nil || actual.State != string(StateRunning) || actual.Netns != parent.Netns {
		t.Fatalf("fresh promotion after historical grant expiry: %+v %v", actual, err)
	}
}

func TestPgInstanceApplicationStandardPausedHistoryCannotRecreateResidency(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardPausedHistoryCannotRecreateResidency(t, s)
}

func TestPgInstanceApplicationStandardPromotionInputChanged(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	standardPromotionInputChanged(t, s)
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM instance_application_standard_promotions WHERE receipt IS NOT NULL`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale inputs persisted promotion receipt: %d %v", count, err)
	}
}

func TestPgInstanceApplicationStandardCommittedPromotionRetryAfterExpiry(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	_, warm, parent := promotionTestParent(t, s)
	p := promotionTestGrant(t, parent)
	p.Binding.ExpiresAtUnixNano = time.Now().Add(300 * time.Millisecond).UnixNano()
	raw, _ := json.Marshal(p.Binding)
	if _, err := pool.Exec(t.Context(), `INSERT INTO instance_application_standard_promotions(token,instance_id,parent_token,binding) VALUES($1,$2,$3,$4::jsonb)`, p.Binding.Token, warm.ID, parent.Binding.Token, raw); err != nil {
		t.Fatal(err)
	}
	r := parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	first, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(time.Unix(0, p.Binding.ExpiresAtUnixNano)) + time.Millisecond)
	again, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r)
	if err != nil || !again.StartedAt.Equal(first.StartedAt) || again.State != string(StateRunning) {
		t.Fatalf("committed authority could not recover lost acknowledgment: %+v %v", again, err)
	}
}

func TestPgInstanceApplicationStandardPromotionRollbackAndRawGuards(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	_, warm, parent := promotionTestParent(t, s)
	p, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent))
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE instance_application_standard_promotions SET parent_token=gen_random_uuid() WHERE instance_id=$1`,
		`UPDATE instance_application_standard_promotions SET binding=jsonb_set(binding,'{payload_hash}','"forged"') WHERE instance_id=$1`,
		`DELETE FROM instance_application_standard_promotions WHERE instance_id=$1`,
		`UPDATE instances SET application_standard_promotion_token=(SELECT token FROM instance_application_standard_promotions WHERE instance_id=$1),state='running' WHERE id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), query, warm.ID); err == nil {
			t.Fatalf("raw writer acquired promotion authority: %s", query)
		}
	}
	_, err = pool.Exec(t.Context(), `CREATE FUNCTION refuse_promotion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='running' THEN RAISE EXCEPTION 'injected publication refusal'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER zz_refuse_promotion BEFORE UPDATE OF state ON instances FOR EACH ROW EXECUTE FUNCTION refuse_promotion();`)
	if err != nil {
		t.Fatal(err)
	}
	r := parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); err == nil {
		t.Fatal("ignored publication failure")
	}
	var received bool
	if err := pool.QueryRow(t.Context(), `SELECT receipt IS NOT NULL FROM instance_application_standard_promotions WHERE instance_id=$1`, warm.ID).Scan(&received); err != nil || received {
		t.Fatalf("failed promotion committed receipt: %v %v", received, err)
	}
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER zz_refuse_promotion ON instances`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE instances SET application_standard_promotion_token=NULL,state='warm' WHERE id=$1`, warm.ID); err == nil {
		t.Fatal("raw writer reused paused parent")
	}
}

func TestPgInstanceApplicationStandardPromotionExclusiveExpiry(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	_, warm, parent := promotionTestParent(t, s)
	p := promotionTestGrant(t, parent)
	p.Binding.ExpiresAtUnixNano = time.Now().Add(300 * time.Millisecond).UnixNano()
	raw, _ := json.Marshal(p.Binding)
	if _, err := pool.Exec(t.Context(), `INSERT INTO instance_application_standard_promotions(token,instance_id,parent_token,binding) VALUES($1,$2,$3,$4::jsonb)`, p.Binding.Token, warm.ID, parent.Binding.Token, raw); err != nil {
		t.Fatal(err)
	}
	r := parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	time.Sleep(time.Until(time.Unix(0, p.Binding.ExpiresAtUnixNano)) + time.Millisecond)
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired promotion published: %v", err)
	}
	raw, _ = json.Marshal(r)
	_, err := pool.Exec(t.Context(), `UPDATE instance_application_standard_promotions SET receipt=$2::jsonb,received_at=clock_timestamp() WHERE instance_id=$1`, warm.ID, raw)
	if !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
		t.Fatalf("raw writer bypassed expiry: %v", err)
	}
	actual, _ := s.InstanceByID(t.Context(), warm.ID)
	if actual.State != string(StateWarm) {
		t.Fatal("expired resume changed serving state")
	}
}

//go:build !no_pg

package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgNativeConsumptionPublication(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	nativeConsumptionPublication(t, s)
}

func TestPgNativeConsumptionRawGrantFence(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	ins, b, capture := nativeConsumedInputs(t, s)
	evidence, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), b.AccountID, b.AppID, b.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	b.ExpiresAtUnixNano = evidence.ExpiresAt.UnixNano()
	insert := func(candidate runtimeadmission.Binding) error {
		raw, _ := json.Marshal(candidate)
		_, err := pool.Exec(t.Context(), `INSERT INTO instance_application_standard_boots(token,instance_id,expected_state,binding) VALUES($1,$2,$3,$4::jsonb)`, candidate.Token, ins.ID, ins.State, raw)
		return err
	}
	if err := insert(b); !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
		t.Fatalf("raw grant escaped registered native capability: %v", err)
	}
	registerConsumedNativeIdentity(t, s, b, runtimeadmission.ArtifactProtocolVersion)
	bad := b
	bad.ArtifactSourcesHash = strings.Repeat("0", 64)
	if err := insert(bad); !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
		t.Fatalf("raw grant escaped captured source membership: %v", err)
	}
	bad = b
	bad.ProtocolVersion = runtimeadmission.ProtocolVersion
	if err := insert(bad); !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
		t.Fatalf("legacy grant acquired consumed source authority: %v", err)
	}
	if err := insert(b); err != nil {
		t.Fatalf("refused grants left partial authority: %v", err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, consumedNativeReceipt(b, capture)); err != nil {
		t.Fatal(err)
	}
}

func TestPgNativeConsumptionRawReceiptFence(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	ins, r := issueConsumedNativeFixture(t, s)
	for _, mutate := range []func(map[string]any){
		func(r map[string]any) { delete(r, "artifact_consumption") },
		func(r map[string]any) { r["method"] = 1 },
		func(r map[string]any) { r["method"] = json.RawMessage("0.0") },
		func(r map[string]any) { r["paused"] = true },
		func(r map[string]any) { r["artifact_consumption"].(map[string]any)["process_pid"] = "42" },
		func(r map[string]any) { r["artifact_consumption"].(map[string]any)["process_start"] = "0101" },
		func(r map[string]any) { r["artifact_consumption"].(map[string]any)["unexpected"] = true },
		func(r map[string]any) { r["artifact_consumption"].(map[string]any)["drives"] = []any{} },
		func(r map[string]any) { nativeRawDrive(r)["root_device"] = false },
		func(r map[string]any) { nativeRawDrive(r)["read_only"] = "true" },
		func(r map[string]any) { nativeRawDrive(r)["producer_digest"] = "sha256:" + strings.Repeat("f", 64) },
		func(r map[string]any) { nativeRawDrive(r)["injected_bytes"] = 0 },
		func(r map[string]any) {
			nativeRawDrive(r)["producer_bytes"] = json.RawMessage(fmt.Sprintf("%.0f.0", nativeRawDrive(r)["producer_bytes"]))
		},
		func(r map[string]any) {
			nativeRawDrive(r)["injected_bytes"] = json.RawMessage(fmt.Sprintf("%.0f.0", nativeRawDrive(r)["injected_bytes"]))
		},
		func(r map[string]any) { nativeRawDrive(r)["unexpected"] = "extra" },
	} {
		raw, _ := json.Marshal(r)
		var changed map[string]any
		if err := json.Unmarshal(raw, &changed); err != nil {
			t.Fatal(err)
		}
		mutate(changed)
		raw, _ = json.Marshal(changed)
		_, err := sqlc.New().RecordInstanceApplicationStandardReceipt(t.Context(), pool, sqlc.RecordInstanceApplicationStandardReceiptParams{Token: mustPgUUID(r.Binding.Token), Receipt: raw})
		if !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
			t.Fatalf("raw malformed consumption escaped native fence: %v", err)
		}
		assertNativeBootUnpublished(t, s, ins)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, r); err != nil {
		t.Fatalf("refusals left partial receipt: %v", err)
	}
}

func nativeRawDrive(r map[string]any) map[string]any {
	return r["artifact_consumption"].(map[string]any)["drives"].([]any)[0].(map[string]any)
}

func TestPgNativeConsumptionPublicationRollback(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	ins, r := issueConsumedNativeFixture(t, s)
	_, err := pool.Exec(t.Context(), `CREATE FUNCTION refuse_consumed_publication() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='running' THEN RAISE EXCEPTION 'injected refusal'; END IF; RETURN NEW; END; $$;
CREATE TRIGGER zz_refuse_consumed_publication BEFORE UPDATE OF state ON instances FOR EACH ROW EXECUTE FUNCTION refuse_consumed_publication();`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, r); err == nil {
		t.Fatal("publication failure ignored")
	}
	assertNativeBootUnpublished(t, s, ins)
	var received bool
	if err := pool.QueryRow(t.Context(), `SELECT receipt IS NOT NULL FROM instance_application_standard_boots WHERE token=$1`, r.Binding.Token).Scan(&received); err != nil || received {
		t.Fatalf("publication refusal committed consumption: %v %v", received, err)
	}
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER zz_refuse_consumed_publication ON instances`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, r); err != nil {
		t.Fatal(err)
	}
}

func TestPgNativeConsumptionSourceHashParity(t *testing.T) {
	_, pool := runtimeCapturePGStore(t)
	sources := []runtimeadmission.ArtifactSource{
		{Kind: "app-layer", StorageKey: "rootfs/παράδειγμα.ext4", Digest: "sha256:" + strings.Repeat("a", 64), Bytes: 17179869184},
		{Kind: "base-image", StorageKey: "base/a.ext4", Digest: "sha256:" + strings.Repeat("b", 64), Bytes: 1},
	}
	for _, name := range []string{"metrics", "aaa", "zzz", "a-1", "0"} {
		sources = append(sources, runtimeadmission.ArtifactSource{Kind: "sidecar-layer", WorkloadName: name, StorageKey: "sidecars/" + name + ".ext4", Digest: "sha256:" + strings.Repeat("c", 64), Bytes: 4096})
	}
	want, err := runtimeadmission.HashArtifactSources(sources)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(sources)
	var actual string
	if err := pool.QueryRow(t.Context(), `SELECT application_standard_native_source_hash($1::jsonb)`, raw).Scan(&actual); err != nil || actual != want {
		t.Fatalf("SQL and native canonical source hashes differ: %s %s %v", actual, want, err)
	}
	for _, key := range []string{".", "/base/a.ext4", "base//a.ext4", "base/./a.ext4", "base/../a.ext4", "base\\a.ext4", "base/a.ext4/"} {
		bad := append([]runtimeadmission.ArtifactSource{}, sources...)
		bad[1].StorageKey = key
		raw, _ := json.Marshal(bad)
		if err := pool.QueryRow(t.Context(), `SELECT application_standard_native_source_hash($1::jsonb)`, raw).Scan(&actual); !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
			t.Fatalf("invalid source key hash accepted: %q %v", key, err)
		}
	}
}

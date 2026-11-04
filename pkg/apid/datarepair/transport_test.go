package datarepair

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSelectiveRepairReviewAssetRoutes(t *testing.T) {
	routes := (&Handler{}).Routes()
	page := httptest.NewRecorder()
	routes.ServeHTTP(page, httptest.NewRequest("GET", "/", nil))
	if page.Code != 200 {
		t.Fatal("review shell unavailable", page.Code)
	}
	assets := regexp.MustCompile(`(?:src|href)="(/[^"?#]+\.(?:js|css))"`).FindAllStringSubmatch(page.Body.String(), -1)
	if len(assets) == 0 {
		t.Fatal("review shell contains no assets")
	}
	for _, asset := range assets {
		t.Run(asset[1], func(t *testing.T) {
			response := httptest.NewRecorder()
			routes.ServeHTTP(response, httptest.NewRequest("GET", asset[1], nil))
			if response.Code != 200 || response.Body.Len() == 0 || !strings.Contains(response.Header().Get("Content-Type"), map[string]string{".js": "javascript", ".css": "text/css"}[asset[1][strings.LastIndex(asset[1], "."):]]) {
				t.Fatalf("review dependency unavailable: %s status=%d type=%s", asset[1], response.Code, response.Header().Get("Content-Type"))
			}
		})
	}
}

func TestSelectiveRepairFullCaptureTransport(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, core.Schema()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE payloads(id bigint PRIMARY KEY,value text NOT NULL); SELECT gregale_repair.enroll('payloads',ARRAY['value']); INSERT INTO payloads(id,value) VALUES(1,'original')`); err != nil {
		t.Fatal(err)
	}
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	// Almost the full journal bound, with HTML characters and Unicode line
	// separators whose JSON representations previously overflowed the wire cap.
	unit := "<>&\u2028\u2029"
	value := strings.Repeat(unit, (api.DataRepairMaxJournalBytes-1024)/len(unit))
	source := uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = core.Capture(ctx, tx, scope, source, "large-record", "payloads", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE payloads SET value=$1`, value)
		return err
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	engine := &core.Engine{Pool: pool}
	token := strings.Repeat("t", 32)
	server := httptest.NewServer((&Handler{Engine: engine, Authenticate: Bearer(token, Principal{Scope: scope, Actor: "reviewer"})}).Routes())
	defer server.Close()
	remote, err := NewRemote(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := remote.Prepare(ctx, scope, source, uuid.NewString())
	if err != nil {
		t.Fatalf("valid full capture could not be prepared through the executor: %v", err)
	}
	reloaded, err := remote.Get(ctx, scope, plan.ID)
	if err != nil || reloaded.Digest != plan.Digest || reloaded.Items[0].AfterJSON != plan.Items[0].AfterJSON {
		t.Fatalf("full capture could not be reloaded exactly: %v", err)
	}
	var after map[string]string
	if err := json.Unmarshal(plan.Items[0].After, &after); err != nil || after["value"] != value {
		t.Fatal("executor transport changed the captured value", err)
	}
	for _, kind := range []string{"managed", "retained"} {
		t.Run(kind, func(t *testing.T) {
			response := httptest.NewRecorder()
			if kind == "managed" {
				r := httptest.NewRequest("GET", "/", nil)
				r.SetPathValue("slug", "products")
				managedReply(response, r, state.Operation{OperationResponse: api.OperationResponse{ID: plan.ID}}, plan)
			} else {
				writePlanJSON(response, HistoricalPlanResponse{Plan: plan})
			}
			if response.Body.Len() > api.DataRepairMaxPlanBytes {
				t.Fatalf("%s response exceeded existing wire cap: %d", kind, response.Body.Len())
			}
			var decoded ManagedResponse
			if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || decoded.Plan.Digest != plan.Digest || decoded.Plan.Items[0].AfterJSON != plan.Items[0].AfterJSON {
				t.Fatalf("%s response changed exact presentation values: %v", kind, err)
			}
		})
	}
	dump := []byte("snapshot-fixture")
	hash := sha256.Sum256(dump)
	input := core.RehearsalInput{Plan: plan, CheckSQL: "SELECT true", Database: "fixture", SnapshotSHA256: hex.EncodeToString(hash[:]), Dump: dump}
	var encoded bytes.Buffer
	if err := core.WriteRehearsalInput(&encoded, input); err != nil {
		t.Fatalf("full capture could not enter the scheduled rehearsal transport: %v", err)
	}
	restored, err := core.ReadRehearsalInput(&encoded)
	var compact bytes.Buffer
	if err := json.Compact(&compact, plan.Items[0].After); err != nil {
		t.Fatal(err)
	}
	if err != nil || !bytes.Equal(restored.Plan.Items[0].After, compact.Bytes()) || restored.Plan.Items[0].AfterJSON != "" {
		t.Fatal("rehearsal transport changed execution inputs or retained presentation duplicates", err)
	}
	if plan.Items[0].AfterJSON == "" {
		t.Fatal("serializing rehearsal inputs mutated the reviewed plan")
	}
}

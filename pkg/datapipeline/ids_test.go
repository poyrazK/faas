package datapipeline

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPipelineChecksAndCreateUseCanonicalUUIDs(t *testing.T) {
	for _, tc := range []struct {
		name string
		form func(string) string
	}{
		{"uppercase", strings.ToUpper},
		{"compact", func(id string) string { return strings.ReplaceAll(id, "-", "") }},
		{"braced", func(id string) string { return "{" + id + "}" }},
		{"urn", func(id string) string { return "urn:uuid:" + id }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, store, _, account := testService(t)
			ctx := t.Context()
			connection, err := service.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:private@db.example/db?sslmode=verify-full"})
			if err != nil {
				t.Fatal(err)
			}
			target := "12345678-90ab-cdef-1234-567890abcdef"
			check, err := service.RequestCheck(ctx, account, connection.ID, api.RequestDataConnectionCheck{ExpectedRevision: connection.Revision, DatabaseID: tc.form(target)})
			if err != nil || check.DatabaseID != target {
				t.Fatal("valid target UUID was not normalized", check, err)
			}
			retryCheck, err := store.RequestCheck(ctx, account, connection.ID, connection.Revision, tc.form(target))
			if err != nil || retryCheck.ID != check.ID || retryCheck.DatabaseID != target {
				t.Fatal("equivalent UUID replaced the active check", retryCheck, err)
			}
			finishSetupCheck(t, store, account, connection, target)
			request := api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: tc.form(connection.ID), DatabaseID: tc.form(target), Tables: []string{"orders"}, ReadSubjects: []string{"alice"}}
			first, err := service.Create(ctx, account, request)
			if err != nil || first.SourceConnectionID != connection.ID || first.DatabaseID != target {
				t.Fatal("target-specific check did not admit equivalent UUIDs", first, err)
			}
			for _, r := range []api.CreateDataPipelineRequest{request, normalize(request)} {
				retry, err := service.Create(ctx, account, r)
				if err != nil || retry.ID != first.ID {
					t.Fatal("equivalent create retry lost its pipeline", retry, err)
				}
			}
			direct, err := store.Create(ctx, Pipeline{AccountID: account, DataPipeline: api.DataPipeline{Name: request.Name, SourceConnectionID: request.SourceConnectionID, DatabaseID: request.DatabaseID, Tables: request.Tables, ReadSubjects: request.ReadSubjects}})
			if err != nil || direct.ID != first.ID {
				t.Fatal("store retry compared raw UUID spellings", direct, err)
			}
			request.DatabaseID = uuid.NewString()
			if _, err = service.Create(ctx, account, request); !errors.Is(err, ErrConflict) {
				t.Fatal("different target adopted an existing pipeline", err)
			}
		})
	}
}

func TestSetupNormalizesCurrentAndLegacyRequestUUIDs(t *testing.T) {
	service, store, account, _, request := setupFixture(t)
	ctx := t.Context()
	request.DatabaseID = uuid.NewString()
	canonical := request
	request.SourceConnectionID = strings.ToUpper(request.SourceConnectionID)
	request.DatabaseID = strings.ToUpper(request.DatabaseID)
	first, err := service.StartSetup(ctx, account, request)
	if err != nil || first.Request.SourceConnectionID != canonical.SourceConnectionID || first.DatabaseID != canonical.DatabaseID || first.Request.DatabaseID != canonical.DatabaseID {
		t.Fatal("setup saved noncanonical IDs", first, err)
	}
	// Older versions persisted raw ID spellings inside immutable request JSON.
	legacy, _ := json.Marshal(request)
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipeline_setups SET request=$2 WHERE id=$1`, first.ID, legacy); err != nil {
		t.Fatal(err)
	}
	for _, r := range []SetupRequest{request, canonical} {
		retry, err := service.StartSetup(ctx, account, r)
		if err != nil || retry.ID != first.ID || retry.Request.SourceConnectionID != canonical.SourceConnectionID {
			t.Fatal("legacy UUID spelling blocked setup recovery", retry, err)
		}
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipeline_setups SET phase='starting' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	finishSetupCheck(t, store, account, api.DataConnection{ID: canonical.SourceConnectionID, Revision: 1}, canonical.DatabaseID)
	p, err := service.Create(ctx, account, api.CreateDataPipelineRequest{Name: request.Name, SourceConnectionID: request.SourceConnectionID, DatabaseID: request.DatabaseID, Tables: request.Tables, ReadSubjects: request.ReadSubjects})
	if err != nil || p.DatabaseID != first.DatabaseID {
		t.Fatal("saved setup and pipeline disagreed on equivalent IDs", p, err)
	}
}

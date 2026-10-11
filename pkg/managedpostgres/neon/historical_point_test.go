package neon

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type fakeHistoricalPointReader struct {
	read func(context.Context, string) (string, error)
}

func (r fakeHistoricalPointReader) ReadLSN(ctx context.Context, dsn string) (string, error) {
	return r.read(ctx, dsn)
}

func TestLSNRestoreResolvesPinnedSourceHistoryAcrossRecovery(t *testing.T) {
	for _, mode := range []string{"create", "discover", "lost_ack", "receipt_restart", "rounded_commit"} {
		t.Run(mode, func(t *testing.T) {
			point := time.Date(2026, 10, 7, 8, 5, 45, 0, time.UTC)
			request := managedpostgres.RestoreRequest{ResourceID: "logical-target", SourceResourceID: "project-source/br-source",
				Spec: testDatabaseSpec(), PointInTime: point, IdempotencyKey: "restore"}
			posts, mappings, custodyWrites := 0, 0, 0
			var provider *Provider
			target := branch{ID: "br-target", ProjectID: "project-source", ParentID: "br-source", ParentLSN: "0/1E25788",
				InitSource: "parent-data", CurrentState: "ready", CreatedAt: point.Add(time.Second).Format(time.RFC3339Nano)}
			if mode == "rounded_commit" {
				target.ParentTimestamp = point.Add(-time.Second).Format(time.RFC3339Nano)
			}
			provider = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				target.Name = provider.restoreBranchName(request.ResourceID)
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v2/projects/project-source/branches":
					branches := []branch{{ID: "br-source", ProjectID: "project-source", CurrentState: "ready"}, {ID: "br-changed-default", Default: true}}
					if mode != "create" && mode != "lost_ack" || posts > 0 {
						branches = append(branches, target)
					}
					writeResponse(t, w, http.StatusOK, map[string]any{"branches": branches})
				case "POST /api/v2/projects/project-source/branches":
					posts++
					if mode == "lost_ack" {
						w.WriteHeader(http.StatusBadGateway)
					} else {
						writeResponse(t, w, http.StatusCreated, map[string]any{"branch": target})
					}
				case "GET /api/v2/projects/project-source/branches/br-target":
					writeResponse(t, w, http.StatusOK, map[string]any{"branch": target})
				case "GET /api/v2/projects/project-source":
					writeResponse(t, w, http.StatusOK, map[string]any{"project": project{ID: "project-source"}})
				case "GET /api/v2/projects/project-source/endpoints":
					writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": []endpoint{{ID: "ep-source", ProjectID: "project-source", BranchID: "br-source",
						RegionID: "aws-eu-central-1", Host: "ep-source.eu-central-1.aws.neon.tech", Type: "read_write", CurrentState: "idle"}}})
				case "GET /api/v2/projects/project-source/connection_uri":
					q := r.URL.Query()
					if q.Get("branch_id") != "br-source" || q.Get("endpoint_id") != "ep-source" || q.Get("pooled") != "false" || q.Get("role_name") != ownerLogin {
						t.Errorf("historical credentials followed mutable defaults: %v", q)
					}
					writeResponse(t, w, http.StatusOK, map[string]any{"uri": "postgresql://" + ownerLogin + ":test-password@ep-source.eu-central-1.aws.neon.tech/gregale?sslmode=require"})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			provider.historicalPoints = fakeHistoricalPointReader{read: func(_ context.Context, dsn string) (string, error) {
				mappings++
				u, err := url.Parse(dsn)
				if err != nil || u.Hostname() != "br-source.eu-central-1.aws.neon.tech" || u.Query().Get("sslmode") != "verify-full" || u.Query().Get("options") != "neon_timestamp:"+point.Format(time.RFC3339Nano) {
					t.Fatal("mapping did not route to the pinned historical source with verified TLS")
				}
				return "00000000/01e25788", nil
			}}
			var accepted *managedpostgres.CreationAcknowledgement
			if mode == "receipt_restart" {
				accepted = &managedpostgres.CreationAcknowledgement{ProviderResourceID: "project-source/br-target", SourceResourceID: request.SourceResourceID, CreatedAt: point.Add(time.Second)}
			}
			observed, err := provider.RestoreWithCreationReceipt(t.Context(), request, accepted, func(_ context.Context, a managedpostgres.CreationAcknowledgement) error {
				custodyWrites++
				if a.ProviderResourceID != "project-source/br-target" || a.SourceResourceID != request.SourceResourceID || mappings != 0 {
					t.Fatal("creation was not checkpointed before historical verification")
				}
				return nil
			})
			if err != nil || observed.RestoreLineage == nil || observed.RestoreLineage.SourceResourceID != request.SourceResourceID || !observed.RestoreLineage.PointInTime.Equal(point) || observed.DataResourceID != "project-source/br-target" {
				t.Fatalf("verified historical restore = %+v, %v", observed, err)
			}
			wantPosts, wantCustody := 0, 0
			if mode == "create" || mode == "lost_ack" {
				wantPosts = 1
			}
			if mode == "create" {
				wantCustody = 1
			}
			if posts != wantPosts || custodyWrites != wantCustody || mappings != 1 {
				t.Fatalf("posts=%d custody=%d mappings=%d", posts, custodyWrites, mappings)
			}
		})
	}
}

func TestLSNRestoreRejectsUnverifiedOrChangedHistory(t *testing.T) {
	point := time.Date(2026, 10, 7, 8, 5, 45, 0, time.UTC)
	for _, fault := range []string{"different_lsn", "unavailable_mapping", "malformed_mapping", "malformed_parent", "zero_parent", "wrong_parent", "wrong_project", "wrong_name", "source_target", "schema_only", "pending", "malformed_timestamp", "contradictory_timestamp", "changed_target", "changed_lsn", "changed_creation"} {
		t.Run(fault, func(t *testing.T) {
			var provider *Provider
			target := branch{ID: "br-target", ProjectID: "project-source", Name: "restore", ParentID: "br-source", ParentLSN: "0/1234",
				InitSource: "parent-data", CurrentState: "ready", CreatedAt: point.Add(time.Second).Format(time.RFC3339Nano)}
			switch fault {
			case "malformed_parent":
				target.ParentLSN = "not-an-lsn"
			case "zero_parent":
				target.ParentLSN = "0/0"
			case "wrong_parent":
				target.ParentID = "br-foreign"
			case "wrong_project":
				target.ProjectID = "foreign-project"
			case "wrong_name":
				target.Name = "foreign-target"
			case "source_target":
				target.ID = "br-source"
			case "schema_only":
				target.InitSource = "parent-schema"
			case "pending":
				target.PendingState = "resetting"
			case "contradictory_timestamp":
				target.ParentTimestamp = point.Add(time.Second).Format(time.RFC3339Nano)
			case "malformed_timestamp":
				target.ParentTimestamp = "not-a-timestamp"
			}
			provider = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/projects/project-source":
					writeResponse(t, w, http.StatusOK, map[string]any{"project": project{ID: "project-source"}})
				case "/api/v2/projects/project-source/branches":
					writeResponse(t, w, http.StatusOK, map[string]any{"branches": []branch{{ID: "br-source", ProjectID: "project-source", CurrentState: "ready"}}})
				case "/api/v2/projects/project-source/endpoints":
					writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": []endpoint{{ID: "ep-source", ProjectID: "project-source", BranchID: "br-source", RegionID: "aws-eu-central-1", Type: "read_write", CurrentState: "idle", Host: "ep-source.eu-central-1.aws.neon.tech"}}})
				case "/api/v2/projects/project-source/connection_uri":
					writeResponse(t, w, http.StatusOK, map[string]any{"uri": "postgresql://" + ownerLogin + ":test@ep-source.eu-central-1.aws.neon.tech/gregale?sslmode=require"})
				case "/api/v2/projects/project-source/branches/br-target":
					confirmed := target
					switch fault {
					case "changed_target":
						confirmed.ID = "br-substitute"
					case "changed_lsn":
						confirmed.ParentLSN = "0/5678"
					case "changed_creation":
						confirmed.CreatedAt = point.Add(2 * time.Second).Format(time.RFC3339Nano)
					}
					writeResponse(t, w, http.StatusOK, map[string]any{"branch": confirmed})
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			provider.historicalPoints = fakeHistoricalPointReader{read: func(context.Context, string) (string, error) {
				switch fault {
				case "different_lsn":
					return "0/5678", nil
				case "unavailable_mapping":
					return "", managedpostgres.ErrUnavailable
				case "malformed_mapping":
					return "invalid", nil
				default:
					return "0/1234", nil
				}
			}}
			observed, err := provider.observeRestoredBranch(t.Context(), "project-source", "br-source", "restore", target, managedpostgres.RestoreRequest{PointInTime: point})
			if err == nil || observed.RestoreLineage != nil || observed.ProviderResourceID != "" {
				t.Fatalf("unverified history adopted: %+v, %v", observed, err)
			}
		})
	}
}

func TestHistoricalDSNRejectsUnpinnedRoutes(t *testing.T) {
	point := time.Now().UTC().Truncate(time.Microsecond)
	for _, fault := range []string{"valid", "pooled", "wrong_host", "foreign_branch", "foreign_project", "foreign_host", "wrong_port", "sub_microsecond"} {
		t.Run(fault, func(t *testing.T) {
			primary := endpoint{ID: "ep-source", ProjectID: "project-source", BranchID: "br-source", Type: "read_write", Host: "ep-source.eu-central-1.aws.neon.tech"}
			material := managedpostgres.CredentialMaterial{Username: ownerLogin, Password: "test", Database: "gregale", Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointDirect, Host: primary.Host, Port: 5432}}}
			p := point
			switch fault {
			case "pooled":
				material.Endpoints[0].Role = managedpostgres.EndpointPooled
			case "wrong_host":
				material.Endpoints[0].Host = "ep-other.eu-central-1.aws.neon.tech"
			case "foreign_branch":
				primary.BranchID = "br-other"
			case "foreign_project":
				primary.ProjectID = "foreign-project"
			case "foreign_host":
				primary.Host = "ep-source.neon.tech.evil.test"
				material.Endpoints[0].Host = primary.Host
			case "wrong_port":
				material.Endpoints[0].Port = 1234
			case "sub_microsecond":
				p = p.Add(time.Nanosecond)
			}
			dsn, err := historicalDSN(material, primary, resourceRef{projectID: "project-source", branchID: "br-source"}, p)
			if fault == "valid" {
				if err != nil || dsn == "" {
					t.Fatal(err)
				}
			} else if !errors.Is(err, managedpostgres.ErrUnavailable) || dsn != "" {
				t.Fatal("unverified route accepted")
			}
		})
	}
}

func TestLSNParseUsesNumericPosition(t *testing.T) {
	for _, value := range []string{"", "0/0", "0", "0/-1", "0/+1", "0/0x1", "0/1/2", "100000000/1", "0/100000000", " 0/1", "0/1 "} {
		if _, err := parseLSN(value); err == nil {
			t.Fatalf("invalid LSN %q accepted", value)
		}
	}
	a, err := parseLSN("fF/00001a")
	b, otherErr := parseLSN("000000FF/1A")
	if err != nil || otherErr != nil || a != b || a != uint64(255)<<32|26 {
		t.Fatal("equivalent WAL positions differ")
	}
}

func TestHistoricalSQLRejectsOrdinaryPrimaryEvenWhenReadOnly(t *testing.T) {
	pool := pgtest.Open(t)
	for _, mode := range []string{"off", "on"} {
		t.Run(mode, func(t *testing.T) {
			// ConnString returns the original input, not later RuntimeParams
			// mutations. Encode the startup parameter in the actual reader DSN.
			dsn := pool.Config().ConnConfig.ConnString()
			if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
				u, err := url.Parse(dsn)
				if err != nil {
					t.Fatal("parse primary fixture DSN")
				}
				q := u.Query()
				q.Set("default_transaction_read_only", mode)
				u.RawQuery = q.Encode()
				dsn = u.String()
			} else {
				dsn += " default_transaction_read_only=" + mode
			}
			conn, err := pgx.Connect(t.Context(), dsn)
			if err != nil {
				t.Fatal("connect primary fixture")
			}
			defer func() { _ = conn.Close(context.Background()) }()
			var actual string
			if err := conn.QueryRow(t.Context(), `SHOW transaction_read_only`).Scan(&actual); err != nil || actual != mode {
				t.Fatal("primary fixture did not apply the requested transaction mode")
			}
			lsn, err := (sqlHistoricalPointReader{}).ReadLSN(t.Context(), dsn)
			if !errors.Is(err, managedpostgres.ErrUnavailable) || lsn != "" {
				t.Fatal("ordinary PostgreSQL primary supplied historical recovery proof")
			}
		})
	}
}

// adr: 569
package neon

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence"
)

type snapshotReaderFixture struct {
	base                          *snapshotRestoreFixture
	request                       managedpostgres.SnapshotCopyReaderRequest
	rows                          []endpoint
	posts, lists, exact, uris     int
	losePost, invisible, ackReady bool
	fault                         string
	uri                           string
}

func newSnapshotReaderFixture(t *testing.T) *snapshotReaderFixture {
	t.Helper()
	b := newSnapshotRestoreFixture(t)
	b.rows = []branch{b.target()}
	capture := b.request
	capture.ExpectedTargetResourceID = "project-source/br-target"
	snapshotAt, _ := time.Parse(time.RFC3339Nano, b.snapshot.CreatedAt)
	captureAt, _ := time.Parse(time.RFC3339Nano, b.rows[0].CreatedAt)
	f := &snapshotReaderFixture{base: b, rows: []endpoint{}, request: managedpostgres.SnapshotCopyReaderRequest{ResourceID: "reader-owner", Capture: capture,
		SnapshotCreatedAt: snapshotAt, CaptureCreatedAt: captureAt, RequestedAt: captureAt.Add(time.Second)}}
	b.p = testProvider(t, http.HandlerFunc(f.serveHTTP))
	return f
}

func (f *snapshotReaderFixture) owned() endpoint {
	settings := endpointSettingsForSpec(f.base.definition.Spec)
	no := false
	return endpoint{ID: "ep-owned", Name: f.base.p.snapshotCopyReaderName(f.request.ResourceID), CreatedAt: f.request.CaptureCreatedAt.Add(time.Minute).Format(time.RFC3339Nano),
		ProjectID: "project-source", BranchID: "br-target", RegionID: "aws-eu-central-1", Type: "read_only", Host: "ep-owned.us-east-1.aws.neon.tech", CurrentState: "active",
		Disabled: &no, PasswordlessAccess: &no, MinimumCU: settings.MinimumCU, MaximumCU: settings.MaximumCU, SuspendTimeoutSecond: settings.SuspendTimeoutSecond}
}

func (f *snapshotReaderFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	const root = "/api/v2/projects/project-source"
	switch {
	case f.fault == "snapshot_disposed" && strings.Contains(r.URL.Path, "/snapshots"):
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodPost && r.URL.Path == root+"/endpoints":
		f.posts++
		var payload snapshotCopyReaderCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			f.base.t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload != snapshotCopyReaderPayload(f.base.p.snapshotCopyReaderName(f.request.ResourceID), "br-target", "aws-eu-central-1", f.base.definition.Spec) {
			f.base.t.Error("creation changed frozen owner/branch/readonly/settings")
		}
		actual := f.owned()
		f.rows = []endpoint{actual}
		if f.fault == "pending" {
			f.rows[0].CurrentState = "init"
			f.rows[0].PendingState = "active"
		}
		if f.losePost {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if f.ackReady {
			actual.CurrentState = "active"
			actual.PendingState = ""
		}
		writeResponse(f.base.t, w, http.StatusCreated, map[string]any{"endpoint": actual, "operations": []operation{}})
	case r.Method == http.MethodGet && r.URL.Path == root+"/endpoints":
		f.lists++
		if len(r.URL.Query()) != 0 {
			f.base.t.Error("invented endpoint pagination/filter")
		}
		if f.fault == "missing_list" {
			writeResponse(f.base.t, w, http.StatusOK, map[string]any{})
			return
		}
		if f.fault == "pagination" {
			writeResponse(f.base.t, w, http.StatusOK, map[string]any{"endpoints": f.rows, "pagination": map[string]any{"cursor": "partial"}})
			return
		}
		rows := f.rows
		if f.invisible {
			rows = []endpoint{}
		}
		writeResponse(f.base.t, w, http.StatusOK, map[string]any{"endpoints": rows})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, root+"/endpoints/"):
		f.exact++
		id := strings.TrimPrefix(r.URL.Path, root+"/endpoints/")
		for _, actual := range f.rows {
			if actual.ID == id {
				if f.fault == "changed_creation" {
					at, _ := time.Parse(time.RFC3339Nano, actual.CreatedAt)
					actual.CreatedAt = at.Add(time.Second).Format(time.RFC3339Nano)
				}
				writeResponse(f.base.t, w, http.StatusOK, map[string]any{"endpoint": actual})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodGet && r.URL.Path == root+"/connection_uri":
		f.uris++
		q := r.URL.Query()
		if q.Get("endpoint_id") != "ep-owned" || q.Get("branch_id") != "br-target" || q.Get("pooled") != "false" || q.Get("database_name") != connectionfence.MaintenanceDatabase || q.Get("role_name") != maintenanceSourceRole {
			f.base.t.Error("URI fell back to default/RW/pooled/source identity")
		}
		uri := f.uri
		if uri == "" {
			uri = "postgres://gregale_owner:reader-private-password@ep-owned.us-east-1.aws.neon.tech:5432/gregale_checkpoint?sslmode=require&options=-c%20search_path%3Dmalicious&application_name=untrusted&hostaddr=169.254.169.254"
		}
		if f.fault == "host_drift" {
			f.rows[0].Host = "ep-owned.replacement.neon.tech"
		}
		writeResponse(f.base.t, w, http.StatusOK, connectionURIResponse{URI: uri})
	default:
		f.base.serveHTTP(w, r)
	}
}

func TestSnapshotCopyReaderCreatesOnlyOwnedReadonlyCaptureAndRecoversLostPost(t *testing.T) {
	for _, lose := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "lost_post"}[lose], func(t *testing.T) {
			f := newSnapshotReaderFixture(t)
			f.losePost = lose
			actual, err := f.base.p.PrepareSnapshotCopyReader(t.Context(), f.base.definition, f.request)
			if err != nil || !actual.Available || actual.EndpointID != "ep-owned" || f.posts != 1 {
				t.Fatalf("reader: %+v %v", actual, err)
			}
			f.request.ExpectedEndpointID, f.request.ExpectedCreatedAt = actual.EndpointID, actual.CreatedAt
			if _, err := f.base.p.FindSnapshotCopyReader(t.Context(), f.base.definition, f.request); err != nil || f.posts != 1 {
				t.Fatalf("pinned discovery repeated POST: %v", err)
			}
		})
	}
}

func TestSnapshotCopyReaderUnknownAbsenceAndIncompleteMetadataNeverRepeatsPost(t *testing.T) {
	f := newSnapshotReaderFixture(t)
	f.losePost = true
	f.invisible = true
	if _, err := f.base.p.PrepareSnapshotCopyReader(t.Context(), f.base.definition, f.request); !errors.Is(err, managedpostgres.ErrUnavailable) || f.posts != 1 {
		t.Fatalf("unknown POST: %v", err)
	}
	if _, err := f.base.p.FindSnapshotCopyReader(t.Context(), f.base.definition, f.request); !errors.Is(err, managedpostgres.ErrNotFound) || f.posts != 1 {
		t.Fatalf("absence created replacement: %v", err)
	}
	f.invisible = false
	if _, err := f.base.p.FindSnapshotCopyReader(t.Context(), f.base.definition, f.request); err != nil || f.posts != 1 {
		t.Fatalf("eventual ownership: %v", err)
	}
	for _, fault := range []string{"missing_list", "pagination"} {
		t.Run(fault, func(t *testing.T) {
			g := newSnapshotReaderFixture(t)
			g.fault = fault
			if _, err := g.base.p.PrepareSnapshotCopyReader(t.Context(), g.base.definition, g.request); !errors.Is(err, managedpostgres.ErrUnavailable) || g.posts != 0 {
				t.Fatalf("incomplete list authorized creation: %v", err)
			}
		})
	}
}

func TestSnapshotCopyReaderRejectsForeignMetadataAndReplacement(t *testing.T) {
	for _, mode := range []string{"project", "branch", "region", "type", "created", "future", "submicrosecond", "size", "duplicate", "duplicate_id", "changed_creation", "known_absent"} {
		t.Run(mode, func(t *testing.T) {
			f := newSnapshotReaderFixture(t)
			actual := f.owned()
			f.rows = []endpoint{actual}
			switch mode {
			case "project":
				f.rows[0].ProjectID = "foreign-project"
			case "branch":
				f.rows[0].BranchID = "br-source"
			case "region":
				f.rows[0].RegionID = "foreign-region"
			case "type":
				f.rows[0].Type = "read_write"
			case "created":
				f.rows[0].CreatedAt = f.request.CaptureCreatedAt.Add(-time.Second).Format(time.RFC3339Nano)
			case "future":
				f.rows[0].CreatedAt = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
			case "submicrosecond":
				f.rows[0].CreatedAt = f.request.CaptureCreatedAt.Add(time.Nanosecond).Format(time.RFC3339Nano)
			case "size":
				f.rows[0].MaximumCU++
			case "duplicate":
				copy := actual
				copy.ID = "ep-other"
				f.rows = append(f.rows, copy)
			case "duplicate_id":
				f.rows = append(f.rows, actual)
			case "changed_creation":
				f.fault = mode
			case "known_absent":
				f.request.ExpectedEndpointID = "ep-missing"
				at, _ := time.Parse(time.RFC3339Nano, actual.CreatedAt)
				f.request.ExpectedCreatedAt = at
			}
			if _, err := f.base.p.PrepareSnapshotCopyReader(t.Context(), f.base.definition, f.request); err == nil || f.posts != 0 {
				t.Fatalf("foreign/changed metadata used: %v", err)
			}
		})
	}
}

func TestSnapshotCopyReaderAvailabilityUsesIndependentReadonlyEndpointState(t *testing.T) {
	f := newSnapshotReaderFixture(t)
	f.fault = "pending"
	f.ackReady = true
	actual, err := f.base.p.PrepareSnapshotCopyReader(t.Context(), f.base.definition, f.request)
	if err != nil || actual.Available || actual.EndpointID == "" || f.posts != 1 {
		t.Fatalf("ack forged availability: %+v %v", actual, err)
	}
	for _, mode := range []string{"disabled", "passwordless", "missing_disabled", "missing_passwordless", "host", "unknown_state", "pending"} {
		t.Run(mode, func(t *testing.T) {
			g := newSnapshotReaderFixture(t)
			ep := g.owned()
			yes := true
			switch mode {
			case "disabled":
				ep.Disabled = &yes
			case "passwordless":
				ep.PasswordlessAccess = &yes
			case "missing_disabled":
				ep.Disabled = nil
			case "missing_passwordless":
				ep.PasswordlessAccess = nil
			case "host":
				ep.Host = "ep-source.neon.tech"
			case "unknown_state":
				ep.CurrentState = "unknown"
			case "pending":
				ep.PendingState = "idle"
			}
			g.rows = []endpoint{ep}
			result, err := g.base.p.FindSnapshotCopyReader(t.Context(), g.base.definition, g.request)
			if err != nil || result.Available || result.EndpointID != ep.ID {
				t.Fatalf("unsafe endpoint readable: %+v %v", result, err)
			}
		})
	}
}

func TestSnapshotCopyReaderConnectionUsesExactEndpointAndStripsURIOptions(t *testing.T) {
	f := newSnapshotReaderFixture(t)
	f.rows = []endpoint{f.owned()}
	f.request.ExpectedEndpointID = "ep-owned"
	f.request.ExpectedCreatedAt = f.request.CaptureCreatedAt.Add(time.Minute)
	cfg, err := f.base.p.snapshotCopyReaderConnectionConfig(t.Context(), f.base.definition, f.request)
	if err != nil {
		t.Fatal(err)
	}
	if f.uris != 1 || f.posts != 0 || cfg.Host != f.rows[0].Host || cfg.Port != 5432 || cfg.Database != connectionfence.MaintenanceDatabase || cfg.User != maintenanceSourceRole || cfg.Password != "reader-private-password" ||
		cfg.TLSConfig == nil || cfg.TLSConfig.InsecureSkipVerify || cfg.TLSConfig.ServerName != cfg.Host || len(cfg.Fallbacks) != 0 || len(cfg.RuntimeParams) != 3 || cfg.RuntimeParams["application_name"] != "gregale-snapshot-copy-reader" || cfg.RuntimeParams["default_transaction_read_only"] != "on" || cfg.RuntimeParams["search_path"] != "pg_catalog" {
		t.Fatal("connection config crossed direct readonly/identity/TLS boundary")
	}
}

func TestSnapshotCopyReaderConnectionRejectsUnpinnedUnsafeAndDriftingURIs(t *testing.T) {
	for _, mode := range []string{"unpinned", "source_host", "pooled", "role", "database", "port", "host_drift", "disabled", "passwordless", "missing_passwordless"} {
		t.Run(mode, func(t *testing.T) {
			f := newSnapshotReaderFixture(t)
			f.rows = []endpoint{f.owned()}
			f.request.ExpectedEndpointID = "ep-owned"
			f.request.ExpectedCreatedAt = f.request.CaptureCreatedAt.Add(time.Minute)
			host, user, db, port := "ep-owned.us-east-1.aws.neon.tech", maintenanceSourceRole, connectionfence.MaintenanceDatabase, "5432"
			yes := true
			switch mode {
			case "unpinned":
				f.request.ExpectedEndpointID = ""
				f.request.ExpectedCreatedAt = time.Time{}
			case "source_host":
				host = "ep-source.neon.tech"
			case "pooled":
				host = "ep-owned-pooler.us-east-1.aws.neon.tech"
			case "role":
				user = "other_role"
			case "database":
				db = "gregale"
			case "port":
				port = "6432"
			case "host_drift":
				f.fault = mode
			case "disabled":
				f.rows[0].Disabled = &yes
			case "passwordless":
				f.rows[0].PasswordlessAccess = &yes
			case "missing_passwordless":
				f.rows[0].PasswordlessAccess = nil
			}
			f.uri = "postgres://" + user + ":private-password@" + host + ":" + port + "/" + db + "?sslmode=require"
			if _, err := f.base.p.snapshotCopyReaderConnectionConfig(t.Context(), f.base.definition, f.request); err == nil || f.posts != 0 {
				t.Fatalf("unsafe URI accepted: %v", err)
			}
			if (mode == "unpinned" || mode == "disabled" || mode == "passwordless" || mode == "missing_passwordless") && f.uris != 0 {
				t.Fatal("credentials read before reader authority")
			}
		})
	}
}

func TestSnapshotCopyReaderDiscoverySurvivesDisposedSourceSnapshot(t *testing.T) {
	f := newSnapshotReaderFixture(t)
	f.rows = []endpoint{f.owned()}
	f.fault = "snapshot_disposed"
	if _, err := f.base.p.PrepareSnapshotCopyReader(t.Context(), f.base.definition, f.request); !errors.Is(err, managedpostgres.ErrNotFound) || f.posts != 0 {
		t.Fatalf("creation ignored disposed snapshot: %v", err)
	}
	o, err := f.base.p.FindSnapshotCopyReader(t.Context(), f.base.definition, f.request)
	if err != nil || !o.Available || o.EndpointID != "ep-owned" || f.posts != 0 {
		t.Fatalf("owned discovery hidden by snapshot disposal: %+v %v", o, err)
	}
	f.base.rows[0].CreatedAt = f.request.CaptureCreatedAt.Add(time.Second).Format(time.RFC3339Nano)
	if _, err := f.base.p.FindSnapshotCopyReader(t.Context(), f.base.definition, f.request); !errors.Is(err, managedpostgres.ErrConflict) || f.posts != 0 {
		t.Fatalf("receipt recovery adopted replacement capture: %v", err)
	}
}

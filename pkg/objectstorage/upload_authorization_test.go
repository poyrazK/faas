package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// These public upload requests bypass apid's HTTP middleware. Exercise actual
// key resolution and restriction rows, including receipt reads under the key.
// adr: 20261104085712
func TestUploadHandlerOrganizationBoundary(t *testing.T) {
	for _, scope := range []string{"legacy", "own organization", "other organization"} {
		t.Run(scope, func(t *testing.T) {
			f, own, _ := scopedUploadFixture(t, false)
			orgID := ""
			if scope == "own organization" {
				orgID = own.ID
			}
			if scope == "other organization" {
				foreign, err := f.store.CreateOrg(t.Context(), state.Org{Slug: "foreign", Name: "Foreign"})
				if err != nil {
					t.Fatal(err)
				}
				if err = f.store.AddOrgMember(t.Context(), foreign.ID, f.account.ID, state.OrgRoleOwner, nil); err != nil {
					t.Fatal(err)
				}
				orgID = foreign.ID
			}
			mintUploadKey(t, &f, orgID, "upload", []string{api.ScopeStorageWrite})
			want := http.StatusCreated
			if scope == "other organization" {
				want = http.StatusUnauthorized
			}
			checkUploadAndReceiptAccess(t, f, want)
		})
	}
}

// adr: 20261104085712
func TestUploadHandlerProjectBoundary(t *testing.T) {
	for _, kind := range []string{"agent", "mcp-login"} {
		for _, scope := range []string{"unlimited", "own project", "other project", "no project"} {
			t.Run(kind+"/"+scope, func(t *testing.T) {
				f, _, project := scopedUploadFixture(t, scope != "no project")
				label, scopes := "agent", []string{api.ScopeAgent, api.ScopeStorageWrite}
				if kind == "mcp-login" {
					label, scopes = api.MCPLoginKeyLabel, []string{api.ScopeStorageWrite}
				}
				key := mintUploadKey(t, &f, "", label, scopes)
				if scope != "unlimited" {
					ids := []string{project.ID}
					if scope == "other project" {
						other, err := f.store.CreateProject(t.Context(), state.Project{AccountID: f.account.ID, OrgID: project.OrgID, Slug: "other"})
						if err != nil {
							t.Fatal(err)
						}
						ids = []string{other.ID}
					}
					if err := f.store.SetAPIKeyProjectLimit(t.Context(), key.ID, ids); err != nil {
						t.Fatal(err)
					}
				}
				want := http.StatusCreated
				if scope == "other project" || scope == "no project" {
					want = http.StatusForbidden
				}
				checkUploadAndReceiptAccess(t, f, want)
			})
		}
	}
}

type unavailableUploadProjectStore struct{ PublicReadStore }

func (s unavailableUploadProjectStore) APIKeyProjectLimit(context.Context, string) ([]string, bool, error) {
	return nil, false, errors.New("project restrictions unavailable")
}

// A missing or failing reader must not turn an unknown restriction into an
// unlimited key, including when an older apid returns the raw key projection.
// adr: 20261104085712
func TestUploadHandlerProjectLookupFailsClosed(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "unavailable", true: "missing reader"}[missing], func(t *testing.T) {
			f, _, _ := scopedUploadFixture(t, true)
			mintUploadKey(t, &f, "", "agent", []string{api.ScopeAgent, api.ScopeStorageWrite})
			h := f.handler.(*uploadHandler)
			if missing {
				h.store = &countingPublicReadStore{Store: f.store}
			} else {
				h.store = unavailableUploadProjectStore{PublicReadStore: f.store}
			}
			checkUploadAndReceiptAccess(t, f, http.StatusServiceUnavailable)
		})
	}
}

func scopedUploadFixture(t *testing.T, inProject bool) (uploadFixture, state.Org, state.Project) {
	t.Helper()
	f := newUploadFixture(t)
	org, err := f.store.CreateOrg(t.Context(), state.Org{Slug: "personal", Name: "Personal", Personal: true, PersonalOwnerAccountID: &f.account.ID})
	if err != nil {
		t.Fatal(err)
	}
	project, err := f.store.CreateProject(t.Context(), state.Project{AccountID: f.account.ID, OrgID: org.ID, Slug: "uploads"})
	if err != nil {
		t.Fatal(err)
	}
	app := state.App{AccountID: f.account.ID, OrgID: org.ID, Slug: "scoped-upload", Status: state.AppActive}
	if inProject {
		app.ProjectID = project.ID
	}
	f.app, err = f.store.CreateApp(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	backend := f.registry.backends["test"]
	b, err := f.store.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: f.account.ID, AppID: f.app.ID, Name: "scoped", Scope: "default", Region: "us-east-1", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "gregale-scoped"}, api.DefaultObjectBucketsPerApp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ClaimObjectBucket(t.Context(), f.account.ID, f.app.ID, b.ID, "scoped", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = f.store.FinishObjectBucket(t.Context(), b.ID, "scoped", "ready"); err != nil {
		t.Fatal(err)
	}
	f.route.ID, f.route.AppID, f.route.BucketID = uuid.NewString(), f.app.ID, b.ID
	if _, err = f.store.UpsertObjectUploadRoute(t.Context(), f.route); err != nil {
		t.Fatal(err)
	}
	return f, org, project
}

func mintUploadKey(t *testing.T, f *uploadFixture, org, label string, scopes []string) state.APIKey {
	t.Helper()
	token, sum, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	var key state.APIKey
	if org == "" {
		key, err = f.store.CreateAPIKey(t.Context(), f.account.ID, sum, label, scopes)
	} else {
		key, err = f.store.CreateOrgAPIKey(t.Context(), org, f.account.ID, sum, label, scopes, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	f.token, f.key = token, key
	return key
}

func checkUploadAndReceiptAccess(t *testing.T, f uploadFixture, want int) {
	t.Helper()
	request := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://"+f.app.Slug+".apps.test"+path, strings.NewReader(""))
		if method == http.MethodPost {
			r = httptest.NewRequest(method, "https://"+f.app.Slug+".apps.test"+path, strings.NewReader("avatar"))
			r.Header.Set("Content-Type", "image/png")
		}
		r.Header.Set("Authorization", "Bearer "+f.token)
		res := httptest.NewRecorder()
		f.handler.ServeHTTP(res, r)
		return res
	}
	res := request(http.MethodPost, "/uploads/avatar")
	if res.Code != want {
		t.Fatalf("upload = %d %s; want %d", res.Code, res.Body.String(), want)
	}
	id, receiptStatus := uuid.NewString(), want
	if want == http.StatusCreated {
		var c struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &c); err != nil || c.ID == "" {
			t.Fatalf("upload receipt = %s, %v", res.Body.String(), err)
		}
		id, receiptStatus = c.ID, http.StatusOK
	} else if f.provider.writes != 0 {
		t.Fatal("refused upload reached the provider")
	}
	if res := request(http.MethodGet, "/uploads/avatar/receipts/"+id); res.Code != receiptStatus {
		t.Fatalf("receipt = %d %s; want %d", res.Code, res.Body.String(), receiptStatus)
	}
}

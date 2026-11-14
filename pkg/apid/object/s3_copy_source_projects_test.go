package object

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// adr: 20261104085712
func TestCopySourceGrantProjectRestrictions(t *testing.T) {
	storageControlStores(t, func(t *testing.T, st state.Store) {
		svc, host, acct, destinationApp, destination := newStorageControlFixture(t, st, "http://127.0.0.1:1")
		project, err := st.CreateProject(t.Context(), state.Project{AccountID: acct.ID, Slug: "other"})
		if err != nil {
			t.Fatal(err)
		}
		var sources []state.ObjectBucket
		for _, projectID := range []string{destinationApp.ProjectID, project.ID, ""} {
			app, err := st.CreateApp(t.Context(), state.App{AccountID: acct.ID, ProjectID: projectID, Slug: "source-" + uuid.NewString(), WorkloadName: "source", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			sources = append(sources, newStorageControlBucket(t, st, host.registry, acct, app, "source"))
		}
		for _, tc := range []struct {
			name                    string
			source                  int
			limited, agent, session bool
			includeSource           bool
			want                    int
		}{
			{name: "mcp same project", limited: true, want: 200},
			{name: "mcp excluded project", source: 1, limited: true, want: 404},
			{name: "mcp permitted second project", source: 1, limited: true, includeSource: true, want: 200},
			{name: "mcp unprojected app", source: 2, limited: true, want: 404},
			{name: "agent excluded project", source: 1, limited: true, agent: true, want: 404},
			{name: "unlimited key", source: 1, want: 200},
			{name: "session", source: 1, session: true, want: 200},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var key *state.APIKey
				if !tc.session {
					_, hash, err := api.GenerateAPIKey()
					if err != nil {
						t.Fatal(err)
					}
					scopes, label := []string{api.ScopeAdmin}, api.MCPLoginKeyLabel
					if tc.agent {
						scopes = append(scopes, api.ScopeAgent)
						label = "agent"
					}
					created, err := st.CreateAPIKey(t.Context(), acct.ID, hash, label, scopes)
					if err != nil {
						t.Fatal(err)
					}
					if tc.limited {
						projects := []string{destinationApp.ProjectID}
						if tc.includeSource {
							projects = append(projects, project.ID)
						}
						if err = st.(state.KeyProjectLimitStore).SetAPIKeyProjectLimit(t.Context(), created.ID, projects); err != nil {
							t.Fatal(err)
						}
						created.ProjectLimited, created.ProjectIDs = true, projects
					}
					key = &created
				}
				credentials := st.(state.ObjectS3CredentialStore)
				accessKeyID, _, err := api.GenerateObjectS3Credential()
				if err != nil {
					t.Fatal(err)
				}
				credential, err := credentials.CreateObjectS3Credential(t.Context(), state.ObjectS3Credential{ID: uuid.NewString(), AccountID: acct.ID, BucketID: destination.ID, AccessKeyID: accessKeyID, SecretSealed: []byte("sealed"), KID: "test", Label: "test", Permission: state.ObjectBucketPermissionReadWrite, Status: state.ObjectS3CredentialStatusActive}, api.MaxObjectS3CredentialsPerBucket)
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(http.MethodPut, "https://api.test/copy-sources", strings.NewReader(`{"prefix":"allowed/"}`))
				r.SetPathValue("slug", destinationApp.Slug)
				r.SetPathValue("bucket", destination.ID)
				r.SetPathValue("credential", credential.ID)
				r.SetPathValue("source", sources[tc.source].ID)
				r = r.WithContext(authmw.WithPrincipal(r.Context(), acct, key, nil))
				w := httptest.NewRecorder()
				svc.SetObjectS3CopySource(w, r, acct)
				grants, err := st.(state.ObjectS3CopySourceStore).ListObjectS3CopySources(t.Context(), acct.ID, destination.ID, credential.ID)
				wantGrants := 0
				if tc.want == 200 {
					wantGrants = 1
				}
				if err != nil || w.Code != tc.want || len(grants) != wantGrants {
					t.Fatal(w.Code, w.Body.String(), grants, err)
				}
				_, _, err = st.(state.ObjectS3CopySourceStore).ResolveObjectS3CopySource(t.Context(), acct.ID, credential.ID, sources[tc.source].ID, "allowed/secret.txt")
				if tc.want == 200 && err != nil || tc.want != 200 && !errors.Is(err, state.ErrNotFound) {
					t.Fatal("unexpected durable source authority", err)
				}
			})
		}
	})
}

type copySourceAppFailure struct {
	MemStore *state.MemStore
	err      error
}

func (f copySourceAppFailure) AppByID(context.Context, string) (state.App, error) {
	return state.App{}, f.err
}

func TestCopySourceProjectLookupFailsClosed(t *testing.T) {
	st := state.NewMemStore()
	svc, host, acct, app, b := newStorageControlFixture(t, st, "http://127.0.0.1:1")
	for _, tc := range []struct {
		err    error
		status int
	}{{state.ErrNotFound, 404}, {errors.New("store unavailable"), 503}} {
		host.store = statetest.NewMem(copySourceAppFailure{MemStore: st, err: tc.err})
		key := state.APIKey{ProjectLimited: true, ProjectIDs: []string{app.ProjectID}}
		r := httptest.NewRequest(http.MethodPut, "https://api.test/", nil)
		r = r.WithContext(authmw.WithPrincipal(r.Context(), acct, &key, nil))
		w := httptest.NewRecorder()
		if svc.authorizeCopySourceProject(w, r, acct, b) || w.Code != tc.status {
			t.Fatal("lookup failure authorized source", w.Code, w.Body.String())
		}
	}
}

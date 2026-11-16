package toolregistry_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestPrivateToolPublicationMemory(t *testing.T) {
	exercisePrivatePublication(t, state.NewMemStore())
}

func TestPrivateToolPublicationLostReplyAndConcurrentRequests(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "concurrent", true: "lost reply"}[lost], func(t *testing.T) {
			f := granttest.NewPublication(t, state.NewMemStore())
			f.Approve(t)
			if lost {
				f.LoseVerificationReply.Store(true)
				_, err := f.Publisher.VerifyToolPublication(t.Context(), f.Publication.ID, f.Request)
				requireGrantStatus(t, err, 503)
			} else {
				results := make(chan error, 8)
				for range 8 {
					go func() {
						_, err := f.Publisher.VerifyToolPublication(t.Context(), f.Publication.ID, f.Request)
						results <- err
					}()
				}
				for range 8 {
					if err := <-results; err != nil {
						var capacity *api.APIError
						if !errors.As(err, &capacity) || capacity.Problem.Code != "publication_verification_capacity" {
							t.Fatal(err)
						}
					}
				}
			}
			f.Wait(t, "completed")
			if f.Calls.Load() != 1 {
				t.Fatalf("duplicate fixture dispatches=%d", f.Calls.Load())
			}
		})
	}
}
func TestPrivateToolPublicationPostgres(t *testing.T) {
	exercisePrivatePublication(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func exercisePrivatePublication(t *testing.T, store state.Store) {
	f := granttest.NewPublication(t, store)
	for range 2 {
		v, err := f.Publisher.VerifyToolPublication(t.Context(), f.Publication.ID, f.Request)
		if err != nil || v.Status != "pending" || f.Calls.Load() != 0 {
			t.Fatalf("unapproved candidate: %+v %v", v, err)
		}
	}
	pending, err := f.Publisher.VerifyToolPublication(t.Context(), f.Publication.ID, f.Request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Publisher.DecideToolPublicationVerification(t.Context(), f.Publication.ID, api.ToolPublicationVerificationDecision{RequestSHA256: pending.RequestSHA256, CredentialSecretRef: "secret:MCP_PUBLICATION_TEST"})
	requireGrantStatus(t, err, 404)
	_, err = f.Owner.DecideToolPublicationVerification(t.Context(), f.Publication.ID, api.ToolPublicationVerificationDecision{RequestSHA256: strings.Repeat("0", 64), CredentialSecretRef: "secret:MCP_PUBLICATION_TEST"})
	requireGrantStatus(t, err, 409)
	f.Approve(t)
	done := f.Wait(t, "completed")
	if f.Calls.Load() != 1 || done.Outcome == nil || !done.Outcome.OK {
		t.Fatalf("private verification: %+v calls=%d", done, f.Calls.Load())
	}
	for range 2 {
		fresh := api.NewClient(f.Native.URL, f.Publisher.Token())
		out, err := fresh.VerifyToolPublication(t.Context(), f.Publication.ID, f.Request)
		if err != nil || out.Status != "completed" || f.Calls.Load() != 1 {
			t.Fatal("verification receipt replayed fixtures", err)
		}
	}
	for _, change := range []func(*api.ToolPublicationVerificationRequest){
		func(in *api.ToolPublicationVerificationRequest) { in.FixturesJSON += " " },
		func(in *api.ToolPublicationVerificationRequest) { in.DeploymentID = uuid.NewString() },
		func(in *api.ToolPublicationVerificationRequest) { in.CatalogSHA256 = strings.Repeat("1", 64) },
	} {
		in := f.Request
		change(&in)
		_, err = f.Publisher.VerifyToolPublication(t.Context(), f.Publication.ID, in)
		requireGrantStatus(t, err, 409)
	}
	req := httptestPublicationRequest(t, f)
	res, err := f.Native.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || strings.Contains(string(raw), f.Token) || strings.Contains(string(raw), "MCP_PUBLICATION_TEST") || strings.Contains(string(raw), "verification_credential") {
		t.Fatal("publisher journal exposed credential metadata")
	}
	out := api.ToolPublicationOutcome{Status: "completed", DeploymentID: f.Candidate.ID, CatalogSHA256: f.Request.CatalogSHA256, Catalog: done.Outcome.Catalog, ProtocolVerified: true, Checks: done.Outcome.Checks}
	forged := out
	forged.CatalogSHA256 = strings.Repeat("2", 64)
	_, err = store.(state.ToolPublicationStore).FinishToolPublication(t.Context(), f.Base.Account.ID, f.Publication.ID, forged, f.Candidate.SourceSHA256)
	if !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("forged native evidence: %v", err)
	}
	if _, err = store.UpdateDeploymentTraffic(t.Context(), f.Candidate.ID, 100, f.Publication.Intent.ExpectedServingDeploymentID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Publisher.FinishToolPublication(t.Context(), f.Publication.ID, out); err != nil {
		t.Fatal(err)
	}
	_, err = f.Base.Agent.GetRegistryTool(t.Context(), f.Publication.ID, "visible")
	requireGrantStatus(t, err, 404)
	g, err := f.Base.Owner.PutToolGrant(t.Context(), uuid.NewString(), api.ToolGrantIntent{KeyID: f.Base.AgentKey.ID, Version: f.Publication.ID, Name: "visible", ExpiresAt: time.Now().UTC().Add(time.Hour), AllowInvocation: true, CredentialSecretRef: "secret:MCP_PUBLICATION_TEST"})
	if err != nil {
		t.Fatal(err)
	}
	desc, err := f.Base.Agent.GetRegistryTool(t.Context(), f.Publication.ID, "visible")
	if err != nil || len(desc.Grants) != 1 {
		t.Fatal("granted private discovery", err)
	}
	args := `{"value":1}`
	in := api.PrivateToolInvocationRequest{GrantID: g.ID, Intent: api.ToolInvocationIntent{Version: f.Publication.ID, Name: "visible", ArgumentsSHA256: toolpublication.Digest([]byte(args)), AccessMode: "external-oauth", CredentialBindingSHA256: g.Credential.SHA256, TimeoutSeconds: 15, AllowToolExecution: true}, ArgumentsJSON: args}
	id := uuid.NewString()
	if _, err = f.Base.Agent.ExecutePrivateToolInvocation(t.Context(), id, in); err != nil {
		t.Fatal(err)
	}
	_ = waitPrivate(t, f.Base.Agent, id, "completed")
	if f.Calls.Load() != 2 {
		t.Fatalf("fixture and invocation effects=%d", f.Calls.Load())
	}
	if _, err = f.Base.Owner.RevokeToolGrant(t.Context(), g.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.Base.Agent.ExecutePrivateToolInvocation(t.Context(), uuid.NewString(), in)
	requireGrantStatus(t, err, 404)
	if f.Calls.Load() != 2 {
		t.Fatal("revoked grant executed")
	}
}

func httptestPublicationRequest(t *testing.T, f *granttest.PublicationFixture) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.Native.URL+"/v1/tool-publications/"+f.Publication.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+f.Publisher.Token())
	return r
}

func TestPrivateToolPublicationCredentialAndFailureFences(t *testing.T) {
	for _, mode := range []string{"fixture failure", "catalog reflection", "protocol failure", "rotate credential", "revoke approval", "revoke publisher", "changed source", "changed serving"} {
		t.Run(mode, func(t *testing.T) {
			f := granttest.NewPublication(t, state.NewMemStore())
			v := f.Approve(t)
			switch mode {
			case "fixture failure":
				f.Mode.Store(1)
			case "catalog reflection":
				f.Mode.Store(2)
			case "protocol failure":
				f.Mode.Store(3)
			case "rotate credential":
				f.RotateTestToken(t, "replacement-publication-token")
			case "revoke approval":
				_, err := f.Owner.DecideToolPublicationVerification(t.Context(), f.Publication.ID, api.ToolPublicationVerificationDecision{RequestSHA256: v.RequestSHA256, Revoke: true})
				if err != nil {
					t.Fatal(err)
				}
			case "revoke publisher":
				if _, err := f.Base.Store.MarkAPIKeyRevoked(t.Context(), f.Base.Account.ID, f.PublisherKey.ID); err != nil {
					t.Fatal(err)
				}
			case "changed source":
				f.Request.DeploymentID = f.Base.PublicationB.Outcome.DeploymentID
			case "changed serving":
				dep, err := f.Base.Store.CreateDeployment(t.Context(), state.Deployment{AppID: f.Base.AppA.ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive, TrafficPercentExplicit: true, SourceSHA256: strings.Repeat("3", 64)})
				if err != nil {
					t.Fatal(err)
				}
				if err = f.Base.Store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployLive, ""); err != nil {
					t.Fatal(err)
				}
				if _, err = f.Base.Store.UpdateDeploymentTraffic(t.Context(), dep.ID, 100); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "revoke approval":
				got, err := f.Publisher.VerifyToolPublication(t.Context(), f.Publication.ID, f.Request)
				if err != nil || got.Status != "revoked" {
					t.Fatalf("revoked approval: %+v %v", got, err)
				}
			case "revoke publisher", "changed source", "changed serving":
				_, err := f.Publisher.VerifyToolPublication(t.Context(), f.Publication.ID, f.Request)
				if err == nil {
					t.Fatal("changed authority accepted")
				}
			default:
				got := f.Wait(t, "failed")
				raw, _ := json.Marshal(got)
				if strings.Contains(string(raw), f.Token) || got.Outcome == nil || got.Outcome.OK || len(got.Outcome.Catalog) != 0 {
					t.Fatal("failed verification retained private catalog or credential")
				}
				for range 2 {
					if _, err := f.Publisher.VerifyToolPublication(t.Context(), f.Publication.ID, f.Request); err != nil {
						t.Fatal(err)
					}
				}
			}
			want := int32(0)
			if mode == "fixture failure" {
				want = 1
			}
			if f.Calls.Load() != want {
				t.Fatalf("effects=%d want=%d", f.Calls.Load(), want)
			}
			candidate, err := f.Base.Store.DeploymentByID(t.Context(), f.Candidate.ID)
			if err != nil || candidate.TrafficPercent != 0 {
				t.Fatal("failed verification promoted candidate", err)
			}
		})
	}
}

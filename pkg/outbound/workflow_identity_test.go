package outbound

import (
	"crypto/ed25519"
	"crypto/rand"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"strings"
	"testing"
	"time"
)

func TestWorkflowIdentityFencesRequestAndSubject(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identity := WorkflowIdentity{AccountID: uuid.NewString(), AppID: uuid.NewString(), RunID: uuid.NewString(), StepName: "crm", Attempt: 1, AttemptToken: uuid.NewString()}
	request := WorkflowOutboundRequest{
		IntegrationID: uuid.NewString(), Method: "POST", Path: "/contacts/a%20b", RawQuery: "email=a%40example.com",
		PathTemplate: "/contacts/{{input.id}}", QueryTemplate: map[string]string{"email": "{{input.email}}"},
	}
	now := time.Now()
	body := []byte(`{"id":1}`)
	token, err := MintWorkflowIdentity(identity, request, body, private, "key-1", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		request WorkflowOutboundRequest
		body    []byte
		key     string
		at      time.Time
		valid   bool
	}{
		{name: "valid", request: request, body: body, key: "key-1", at: now, valid: true},
		{name: "wrong integration", request: func() WorkflowOutboundRequest { r := request; r.IntegrationID = uuid.NewString(); return r }(), body: body, key: "key-1", at: now},
		{name: "wrong method", request: func() WorkflowOutboundRequest { r := request; r.Method = "GET"; return r }(), body: body, key: "key-1", at: now},
		{name: "wrong path", request: func() WorkflowOutboundRequest { r := request; r.Path = "/admin"; return r }(), body: body, key: "key-1", at: now},
		{name: "wrong query", request: func() WorkflowOutboundRequest { r := request; r.RawQuery = "email=other%40example.com"; return r }(), body: body, key: "key-1", at: now},
		{name: "wrong body", request: request, body: []byte(`{"id":2}`), key: "key-1", at: now},
		{name: "retired key", request: request, body: body, key: "key-2", at: now},
		{name: "expired", request: request, body: body, key: "key-1", at: now.Add(time.Minute)},
		{name: "future", request: request, body: body, key: "key-1", at: now.Add(-time.Minute)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, verified, err := verifyWorkflowIdentity(token, test.request, test.body, test.key, public, test.at)
			if (err == nil) != test.valid {
				t.Fatalf("identity=%#v request=%#v error=%v", got, verified, err)
			}
			if test.valid && (got != identity || verified.PathTemplate != request.PathTemplate || verified.QueryTemplate["email"] != request.QueryTemplate["email"]) {
				t.Fatalf("verified route template = %#v, identity = %#v", verified, got)
			}
		})
	}
	internal, err := internalsvc.Mint("schedd", 30*time.Second, map[string]any{"run_id": identity.RunID}, private, "key-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := verifyWorkflowIdentity(internal, request, body, "key-1", public, now); err == nil {
		t.Fatal("accepted an ordinary internal service assertion")
	}

	identity.PlatformTenantID = uuid.NewString()
	tenantRequest := WorkflowOutboundRequest{IntegrationID: uuid.NewString(), Method: "POST", Path: "/contacts", PathTemplate: "/contacts", QueryTemplate: map[string]string{}}
	tenantToken, err := MintWorkflowIdentity(identity, tenantRequest, body, private, "key-1", now)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := verifyWorkflowIdentity(tenantToken, tenantRequest, body, "key-1", public, now)
	if err != nil || got.PlatformTenantID != identity.PlatformTenantID {
		t.Fatalf("tenant identity=%+v error=%v", got, err)
	}
	identity.PlatformTenantID = "not-a-uuid"
	if _, err := MintWorkflowIdentity(identity, tenantRequest, body, private, "key-1", now); err == nil {
		t.Fatal("minted workflow identity with malformed tenant id")
	}
}

func TestWorkflowIdentityAcceptsLargestBoundedOutboundTemplate(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identity := WorkflowIdentity{AccountID: uuid.NewString(), AppID: uuid.NewString(), RunID: uuid.NewString(), StepName: "crm", Attempt: 1, AttemptToken: uuid.NewString()}
	path := "/" + strings.Repeat("a", 2047)
	queryValue := strings.Repeat("x", 2048)
	request := WorkflowOutboundRequest{
		IntegrationID: uuid.NewString(), Method: "GET", Path: path, RawQuery: "q=" + queryValue,
		PathTemplate: path, QueryTemplate: map[string]string{"q": queryValue},
	}
	now := time.Now()
	token, err := MintWorkflowIdentity(identity, request, nil, private, "key-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) <= 8192 || len(token) > workflowIdentityMaxTokenBytes {
		t.Fatalf("large but bounded request produced unexpected token size %d", len(token))
	}
	if _, _, err := verifyWorkflowIdentity(token, request, nil, "key-1", public, now); err != nil {
		t.Fatalf("rejected largest bounded request token: %v", err)
	}
}

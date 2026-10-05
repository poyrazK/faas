package outbound

import (
	"crypto/ed25519"
	"crypto/rand"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"testing"
	"time"
)

func TestWorkflowIdentityFencesRequestAndSubject(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identity := WorkflowIdentity{AccountID: uuid.NewString(), AppID: uuid.NewString(), RunID: uuid.NewString(), StepName: "crm", Attempt: 1, AttemptToken: uuid.NewString()}
	integration := uuid.NewString()
	now := time.Now()
	body := []byte(`{"id":1}`)
	token, err := MintWorkflowIdentity(identity, integration, "POST", "/contacts", body, private, "key-1", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name                         string
		token, id, method, path, key string
		body                         []byte
		at                           time.Time
		valid                        bool
	}{
		{"valid", token, integration, "POST", "/contacts", "key-1", body, now, true},
		{"wrong integration", token, uuid.NewString(), "POST", "/contacts", "key-1", body, now, false},
		{"wrong method", token, integration, "GET", "/contacts", "key-1", body, now, false},
		{"wrong path", token, integration, "POST", "/admin", "key-1", body, now, false},
		{"wrong body", token, integration, "POST", "/contacts", "key-1", []byte(`{"id":2}`), now, false},
		{"retired key", token, integration, "POST", "/contacts", "key-2", body, now, false},
		{"expired", token, integration, "POST", "/contacts", "key-1", body, now.Add(time.Minute), false},
		{"future", token, integration, "POST", "/contacts", "key-1", body, now.Add(-time.Minute), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := verifyWorkflowIdentity(test.token, test.id, test.method, test.path, test.body, test.key, public, test.at)
			if (err == nil) != test.valid {
				t.Fatalf("identity=%#v error=%v", got, err)
			}
		})
	}
	internal, err := internalsvc.Mint("schedd", 30*time.Second, map[string]any{"run_id": identity.RunID}, private, "key-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyWorkflowIdentity(internal, integration, "POST", "/contacts", body, "key-1", public, now); err == nil {
		t.Fatal("accepted an ordinary internal service assertion")
	}

	identity.PlatformTenantID = uuid.NewString()
	tenantToken, err := MintWorkflowIdentity(identity, integration, "POST", "/contacts", body, private, "key-1", now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := verifyWorkflowIdentity(tenantToken, integration, "POST", "/contacts", body, "key-1", public, now)
	if err != nil || got.PlatformTenantID != identity.PlatformTenantID {
		t.Fatalf("tenant identity=%+v error=%v", got, err)
	}
	identity.PlatformTenantID = "not-a-uuid"
	if _, err := MintWorkflowIdentity(identity, integration, "POST", "/contacts", body, private, "key-1", now); err == nil {
		t.Fatal("minted workflow identity with malformed tenant id")
	}
}

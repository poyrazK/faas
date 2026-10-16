// adr: 590
package apidserver

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func checkpointPgAppSecretFixture(t *testing.T) (*age.X25519Identity, state.ProjectEnvironmentCloneCheckpointPgActivationBinding, state.ProjectEnvironmentCloneCheckpointPgConnectionValue, state.ProjectEnvironmentCloneCheckpointPostgresCredential) {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	oldRecipient, oldIdentities, oldHMAC := setSecretRecipient, mfaIdentities, hostHMACKey
	t.Cleanup(func() { setSecretRecipient, mfaIdentities, hostHMACKey = oldRecipient, oldIdentities, oldHMAC })
	setSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	hostHMACKey = func() []byte { return []byte("checkpoint-test-hmac-key") }
	p := state.ProjectEnvironmentCloneCheckpointPostgresCredentialPlan{SourceBindingID: "source-binding", TargetBindingID: "target-binding", ProviderIdentityID: "stage-target-binding", AppID: "app", Scope: "stage", EnvironmentKey: "DATABASE_URL", Access: "read_only", CredentialGeneration: 1}
	p.Runtime.Verification.Isolation.AccountID = "account"
	kid, cipher, err := sealCheckpointPostgresCredential(p)
	if err != nil {
		t.Fatal(err)
	}
	credential := state.ProjectEnvironmentCloneCheckpointPostgresCredential{Plan: p, State: "prepared", KeyID: kid, Ciphertext: cipher, CiphertextSHA256: state.ProjectEnvironmentCloneCheckpointPostgresCredentialCiphertextSHA256(cipher), ObservationSHA256: strings.Repeat("a", 64)}
	plan := state.ProjectEnvironmentCloneCheckpointPgConnectionPlan{Credential: p, CredentialCiphertextSHA256: credential.CiphertextSHA256, CredentialObservationSHA256: credential.ObservationSHA256, Endpoint: state.ProjectEnvironmentCloneCheckpointPgEndpointObservation{Host: "target.example", Database: "stage_db", Port: 5432}}
	kid, cipher, err = sealCheckpointPgConnectionValue(plan, credential)
	if err != nil {
		t.Fatal(err)
	}
	in := state.ProjectEnvironmentCloneCheckpointPgConnectionValue{Plan: plan, State: "sealed", KeyID: kid, Ciphertext: cipher, CiphertextSHA256: state.ProjectEnvironmentCloneCheckpointPostgresCredentialCiphertextSHA256(cipher)}
	member := state.ProjectEnvironmentCloneCheckpointPgActivationBinding{Connection: plan, ConnectionKeyID: kid, ConnectionCiphertextSHA256: in.CiphertextSHA256}
	return identity, member, in, credential
}

func TestCheckpointPgAppSecretRoundTripAndRotation(t *testing.T) {
	original, member, in, credential := checkpointPgAppSecretFixture(t)
	current, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	setSecretRecipient = func() *age.X25519Recipient { return current.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current, original} }
	out, err := sealCheckpointPgAppSecret(member, in, credential)
	if err != nil {
		t.Fatal(err)
	}
	values, err := secretbox.Open(current, out.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || out.Kid != current.Recipient().String() || out.AccountID != "account" || out.AppID != "app" || out.Scope != "stage" || out.ManagedPostgresBindingID != "target-binding" || out.ManagedPostgresAccess != "read_only" || out.ManagedCredentialGeneration != 1 || out.SecretVersion != 1 || out.SecretClass != state.SecretClassPersistent {
		t.Fatal("lost original secret ownership")
	}
	uri, err := url.Parse(values["DATABASE_URL"])
	if err != nil {
		t.Fatal(err)
	}
	password, err := openCheckpointPostgresCredential(credential)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(password.value)
	actual, _ := uri.User.Password()
	if uri.User.Username() != credential.Plan.ProviderIdentityID || actual != string(password.value) || uri.Host != "target.example:5432" || uri.Path != "/stage_db" || uri.RawQuery != "sslmode=verify-full" {
		t.Fatal("changed original connection")
	}
	fingerprint, err := secretbox.ValueFingerprint([]byte(values["DATABASE_URL"]), hostHMACKey())
	if err != nil || fingerprint != out.ValueHash {
		t.Fatal("invalid application value fingerprint")
	}
	if _, err = secretbox.Open(original, out.Ciphertext); err == nil {
		t.Fatal("app secret used original rather than current recipient")
	}
}

func TestCheckpointPgAppSecretRejectsForeignEnvelopes(t *testing.T) {
	for _, kind := range []string{"manifest", "key", "ciphertext", "credential", "unsealed", "namespace", "plan", "password", "endpoint", "tls", "missing-key", "missing-hmac"} {
		t.Run(kind, func(t *testing.T) {
			identity, member, in, credential := checkpointPgAppSecretFixture(t)
			switch kind {
			case "manifest":
				member.Connection.Credential.AppID = "foreign"
			case "key":
				member.ConnectionKeyID = "foreign"
			case "ciphertext":
				in.Ciphertext[0] ^= 1
			case "credential":
				credential.ObservationSHA256 = "foreign"
			case "unsealed":
				in.State = "planned"
			case "missing-key":
				mfaIdentities = func() []*age.X25519Identity { return nil }
			case "missing-hmac":
				hostHMACKey = func() []byte { return nil }
			default:
				namespace, raw, err := secretbox.OpenBytes(identity, in.Ciphertext)
				if err != nil {
					t.Fatal(err)
				}
				defer clear(raw)
				var envelope checkpointPgConnectionEnvelope
				if json.Unmarshal(raw, &envelope) != nil {
					t.Fatal("invalid fixture")
				}
				defer clear(envelope.ConnectionValue)
				switch kind {
				case "namespace":
					namespace = "foreign"
				case "plan":
					envelope.PlanSHA256 = "foreign"
				default:
					uri, err := url.Parse(string(envelope.ConnectionValue))
					if err != nil {
						t.Fatal(err)
					}
					switch kind {
					case "password":
						uri.User = url.UserPassword(uri.User.Username(), "foreign-password")
					case "endpoint":
						uri.Host = "source.example:5432"
					case "tls":
						uri.RawQuery = "sslmode=disable"
					}
					envelope.ConnectionValue = []byte(uri.String())
				}
				raw, err = json.Marshal(envelope)
				if err != nil {
					t.Fatal(err)
				}
				defer clear(raw)
				in.Ciphertext, err = secretbox.SealBytes(identity.Recipient(), namespace, raw, api.PostgresCheckpointConnectionCiphertextMaxBytes)
				if err != nil {
					t.Fatal(err)
				}
				in.CiphertextSHA256 = state.ProjectEnvironmentCloneCheckpointPostgresCredentialCiphertextSHA256(in.Ciphertext)
				member.ConnectionCiphertextSHA256 = in.CiphertextSHA256
			}
			out, err := sealCheckpointPgAppSecret(member, in, credential)
			if !errors.Is(err, errCloneCheckpointUnavailable) || len(out.Ciphertext) != 0 || strings.Contains(err.Error(), "foreign-password") || strings.Contains(err.Error(), "source.example") {
				t.Fatal("foreign envelope accepted or disclosed")
			}
		})
	}
}

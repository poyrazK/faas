package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/dataapiinspect"
)

type expectationTestProvider struct {
	*bindingProvider
	resolve func(CredentialRequest, string) (DataAPIBindingTarget, error)
}

func TestDataAPIBindingExpectationComparesTargetWithoutClaimingProvenance(t *testing.T) {
	expectation := DataAPIBindingExpectation{Target: DataAPIBindingTarget{Host: "db.example", Port: 5432, Database: "app", Login: "binding"}}
	nonce := strings.Repeat("a", 64)
	digest, err := dataapiinspect.TargetDigest(nonce, "db.example", 5432, "app", "binding")
	if err != nil {
		t.Fatal(err)
	}
	matched, err := expectation.MatchesObservedTarget(nonce, digest)
	if err != nil || !matched {
		t.Fatalf("expected target did not match: %v", err)
	}
	other, _ := dataapiinspect.TargetDigest(nonce, "other.example", 5432, "app", "binding")
	matched, err = expectation.MatchesObservedTarget(nonce, other)
	if err != nil || matched {
		t.Fatalf("wrong target matched: %v", err)
	}
	if _, err := expectation.MatchesObservedTarget(nonce, "bad"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid digest: %v", err)
	}
	if matched, err := expectation.MatchesObservedTarget(strings.Repeat("b", 64), digest); err != nil || matched {
		t.Fatalf("different nonce matched: %v", err)
	}
}

func TestDataAPIBindingExpectationComparesConfiguredTrust(t *testing.T) {
	expectation := DataAPIBindingExpectation{Target: DataAPIBindingTarget{TrustBundleSHA256: strings.Repeat("a", 64)}}
	if matched, err := expectation.MatchesObservedTrust(strings.Repeat("a", 64)); err != nil || !matched {
		t.Fatalf("same trust: %v %v", matched, err)
	}
	if matched, err := expectation.MatchesObservedTrust(strings.Repeat("b", 64)); err != nil || matched {
		t.Fatalf("different trust: %v %v", matched, err)
	}
	for _, bad := range []string{"", strings.Repeat("A", 64), strings.Repeat("x", 64)} {
		if matched, err := expectation.MatchesObservedTrust(bad); matched || !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid observed trust %q: %v %v", bad, matched, err)
		}
	}
	expectation.Target.TrustBundleSHA256 = ""
	if matched, err := expectation.MatchesObservedTrust(strings.Repeat("a", 64)); matched || !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing provider trust: %v %v", matched, err)
	}
}

func TestInspectionTrustDoesNotChangeDatabasePlacement(t *testing.T) {
	backend := BackendConfig{Driver: "firecracker", Region: "eu-central", Settings: map[string]string{"host_suffix": "db.example"}}
	original := fingerprint(backend)
	backend.DataAPIInspectionTrustSHA256 = strings.Repeat("a", 64)
	if fingerprint(backend) != original {
		t.Fatal("inspection trust changed the database placement fingerprint")
	}
}

func (p *expectationTestProvider) DataAPIBindingTarget(_ context.Context, req CredentialRequest, identity string, generation int64) (DataAPIBindingTarget, error) {
	if !ValidDataAPIBindingCredentialRequest(req, generation) {
		return DataAPIBindingTarget{}, ErrInvalid
	}
	return p.resolve(req, identity)
}

func TestDataAPIBindingExpectationUsesProviderAndFencesChanges(t *testing.T) {
	for _, scenario := range []string{"expected", "rotating", "wrong_app", "database_not_current", "binding_changed", "database_changed", "unsupported", "provider_conflict"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
			enabled := true
			base := &bindingProvider{}
			store := &failFinishBindingStore{MemoryStore: NewMemoryStore()}
			service, memory, database := readyBindingFixture(t, base, store, &now, &enabled)
			binding, err := service.Create(t.Context(), CreateBindingRequest{AccountID: "account-a", DatabaseID: database.ID, AppID: "app-a", Scope: "default", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite})
			if err != nil {
				t.Fatal(err)
			}
			binding.Access = CredentialDataAPI
			if scenario == "rotating" {
				binding.RotationPreviousGeneration = 1
			}
			memory.bindings[binding.ID] = binding
			if scenario == "database_not_current" {
				changed := memory.databases[database.ID]
				changed.DesiredGeneration++
				memory.databases[database.ID] = changed
			}
			calls := 0
			provider := &expectationTestProvider{bindingProvider: base, resolve: func(req CredentialRequest, identity string) (DataAPIBindingTarget, error) {
				calls++
				if req.BindingID != binding.ID || req.IdentityKey != bindingCredentialIdentity(binding) || req.ProviderResourceID != databaseDataResource(database) || req.Access != CredentialDataAPI || identity != binding.ProviderIdentityID {
					t.Fatal("provider did not receive the pinned binding identity")
				}
				switch scenario {
				case "binding_changed":
					changed := memory.bindings[binding.ID]
					changed.CredentialGeneration++
					memory.bindings[binding.ID] = changed
				case "database_changed":
					changed := memory.databases[database.ID]
					changed.UpdatedAt = changed.UpdatedAt.Add(time.Second)
					memory.databases[database.ID] = changed
				case "provider_conflict":
					return DataAPIBindingTarget{}, ErrConflict
				}
				return DataAPIBindingTarget{Host: "private.example", Port: 5432, Database: "db", Login: "role"}, nil
			}}
			base.capabilities.CredentialAccess = append(base.capabilities.CredentialAccess, CredentialDataAPI)
			if scenario != "unsupported" {
				service.registry = testRegistry(t, provider, func(config *Config) { config.ProvisioningEnabled = true })
			} else {
				service.registry = testRegistry(t, base, func(config *Config) { config.ProvisioningEnabled = true })
			}
			app := "app-a"
			if scenario == "wrong_app" {
				app = "other"
			}
			got, err := service.DataAPIBindingExpectation(t.Context(), "account-a", app, database.ID, binding.ID)
			switch scenario {
			case "expected":
				if err != nil || calls != 1 || got.BindingID != binding.ID || got.CredentialGeneration != binding.CredentialGeneration || got.Target.Login != "role" {
					t.Fatalf("unexpected expectation or error: %+v %v", got, err)
				}
				if encoded, marshalErr := json.Marshal(got); marshalErr != nil || string(encoded) != "{}" {
					t.Fatalf("private expectation was serialized: %s %v", encoded, marshalErr)
				}
			case "unsupported":
				if !errors.Is(err, ErrUnsupported) || calls != 0 {
					t.Fatalf("unsupported provider returned %v", err)
				}
			default:
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("%s returned %v, want conflict", scenario, err)
				}
			}
		})
	}
}

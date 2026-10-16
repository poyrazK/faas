package hosting

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCloudflareDomainProvisioningRequiresExplicitRecovery(t *testing.T) {
	policy := uuid.NewString()
	provider, err := NewCloudflareDomains(strings.Repeat("a", 32), strings.Repeat("b", 32), policy, "test-secret", "customers.hosting.example")
	if err != nil {
		t.Fatal(err)
	}
	domain := api.HostingDomain{ID: uuid.NewString(), Hostname: "portal.customer.example", Details: api.HostingDomainDetails{TLSStatus: "pending"}}
	var host *cfDomainHostname
	var app *cfDomainApp
	creates := 0
	appCreates := 0
	patches := 0
	deletes := 0
	rejectResponse := false
	zoneMismatch := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing provider credential")
		}
		response := func(result any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result, "result_info": map[string]int{"total_pages": 1}})
		}
		if r.URL.Path == "/zones/"+provider.zone {
			name := "hosting.example"
			if zoneMismatch {
				name = "gregale.dev"
			}
			response(map[string]any{"name": name, "account": map[string]string{"id": provider.account}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/fallback_origin") {
			response(map[string]string{"origin": provider.target, "status": "active"})
			return
		}
		isHost := strings.Contains(r.URL.Path, "custom_hostnames")
		switch r.Method {
		case "GET":
			if isHost {
				if strings.HasSuffix(r.URL.Path, "custom_hostnames") {
					result := []cfDomainHostname{}
					if host != nil {
						result = append(result, *host)
					}
					response(result)
				} else if host != nil {
					response(host)
				} else {
					w.WriteHeader(404)
				}
			} else if strings.HasSuffix(r.URL.Path, "apps") {
				result := []cfDomainApp{}
				if app != nil {
					result = append(result, *app)
				}
				response(result)
			} else if app != nil {
				response(app)
			} else {
				w.WriteHeader(404)
			}
		case "POST":
			if isHost {
				creates++
				var body struct {
					Hostname string            `json:"hostname"`
					Metadata map[string]string `json:"custom_metadata"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.Metadata != nil {
					t.Error("standard-plan create sent Enterprise custom metadata")
				}
				host = &cfDomainHostname{ID: uuid.NewString(), Hostname: body.Hostname, Status: "pending", Metadata: body.Metadata, Ownership: api.HostingDNSRecord{Type: "txt", Name: "_cf-custom-hostname." + domain.Hostname, Value: "proof"}}
				host.SSL.Type = "dv"
				host.SSL.Method = "txt"
				host.SSL.Status = "pending_validation"
				if rejectResponse {
					w.WriteHeader(503)
					return
				}
				response(host)
			} else {
				appCreates++
				var body cfDomainApp
				_ = json.NewDecoder(r.Body).Decode(&body)
				body.ID = uuid.NewString()
				body.AUD = strings.Repeat("c", 64)
				app = &body
				response(app)
			}
		case "PATCH":
			patches++
			host.SSL.Status = "pending_validation"
			response(host)
		case "DELETE":
			deletes++
			if isHost {
				host = nil
			} else {
				app = nil
			}
			response(map[string]string{"id": "removed"})
		}
	}))
	defer server.Close()
	provider.base = server.URL
	provider.client = server.Client()
	zoneMismatch = true
	if _, _, err := provider.Ensure(t.Context(), domain, domain.Details.CreateStartedAt == nil); !errors.Is(err, ErrDomainProviderConflict) || creates != 0 {
		t.Fatalf("wrote outside isolated zone: %v", err)
	}
	zoneMismatch = false
	rejectResponse = true
	if _, _, err := provider.Ensure(t.Context(), domain, domain.Details.CreateStartedAt == nil); !errors.Is(err, ErrDomainProviderUnavailable) {
		t.Fatalf("lost response: %v", err)
	}
	rejectResponse = false
	if _, _, err := provider.Ensure(t.Context(), domain, true); !errors.Is(err, ErrDomainProviderConflict) || creates != 1 {
		t.Fatalf("adopted matching unbound hostname: %v", err)
	}
	if err := provider.ValidateRecovery(t.Context(), domain, uuid.NewString()); !errors.Is(err, ErrDomainProviderConflict) {
		t.Fatalf("accepted wrong provider ID: %v", err)
	}
	originalHostname := host.Hostname
	host.Hostname = "foreign.customer.example"
	if err := provider.ValidateRecovery(t.Context(), domain, host.ID); !errors.Is(err, ErrDomainProviderConflict) {
		t.Fatalf("accepted foreign hostname: %v", err)
	}
	host.Hostname = originalHostname
	at := time.Now().UTC()
	domain.Details.CreateStartedAt = &at
	if _, _, err := provider.Ensure(t.Context(), domain, false); !errors.Is(err, ErrDomainProviderConflict) || creates != 1 {
		t.Fatalf("repeated ambiguous create: %v", err)
	}
	if err := provider.Remove(t.Context(), domain); !errors.Is(err, ErrDomainProviderConflict) || deletes != 0 {
		t.Fatalf("purged unresolved create: %v", err)
	}
	if err := provider.ValidateRecovery(t.Context(), domain, host.ID); err != nil {
		t.Fatal(err)
	}
	domain.Details.ProviderID = host.ID
	details, ready, err := provider.Ensure(t.Context(), domain, domain.Details.CreateStartedAt == nil)
	if err != nil || ready || creates != 1 || len(details.Records) != 1 || details.Records[0].Type != "TXT" || appCreates != 0 {
		t.Fatalf("pending recovery: %+v %v %v", details, ready, err)
	}
	domain.Details = details
	host.SSL.Status = "validation_timed_out"
	details, ready, err = provider.Ensure(t.Context(), domain, domain.Details.CreateStartedAt == nil)
	if err != nil || ready || patches != 1 || details.ValidationRetriedAt == nil {
		t.Fatalf("validation retry: %+v %v", details, err)
	}
	domain.Details = details
	host.SSL.Status = "validation_timed_out"
	if _, _, err = provider.Ensure(t.Context(), domain, domain.Details.CreateStartedAt == nil); err != nil || patches != 1 {
		t.Fatalf("validation retry storm: %d %v", patches, err)
	}
	host.Status = "active"
	host.SSL.Status = "active"
	details, ready, err = provider.Ensure(t.Context(), domain, domain.Details.CreateStartedAt == nil)
	if err != nil || !ready || details.AccessAUD == "" || appCreates != 1 {
		t.Fatalf("ready: %+v %v %v", details, ready, err)
	}
	// Simulate loss of the API/SQL receipt for Access creation too.
	if _, ready, err = provider.Ensure(t.Context(), domain, domain.Details.CreateStartedAt == nil); err != nil || !ready || appCreates != 1 {
		t.Fatalf("Access recovery: %v %v", ready, err)
	}
	host.Metadata = map[string]string{"gregale_domain_id": "foreign"}
	if _, _, err = provider.Ensure(t.Context(), domain, domain.Details.CreateStartedAt == nil); !errors.Is(err, ErrDomainProviderConflict) {
		t.Fatalf("adopted foreign hostname: %v", err)
	}
	if err = provider.Remove(t.Context(), domain); !errors.Is(err, ErrDomainProviderConflict) || deletes != 0 {
		t.Fatalf("deleted foreign provider object: %v", err)
	}
	host.Metadata = nil
	domain.Details = details
	if err = provider.Remove(t.Context(), domain); err != nil || deletes != 2 {
		t.Fatalf("cleanup: %v deletes=%d", err, deletes)
	}
	if err = provider.Remove(t.Context(), domain); err != nil || deletes != 2 {
		t.Fatalf("cleanup retry: %v", err)
	}
}

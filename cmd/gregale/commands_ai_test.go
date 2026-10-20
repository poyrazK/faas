package main_test

// adr: 990 — euro amounts become integer millicents without floating point.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestParseEuroMillicents(t *testing.T) {
	for in, want := range map[string]int64{
		"0": 0, "12": 1_200_000, "12.5": 1_250_000, "0.15": 15_000, ".075": 7_500, "0.00001": 1,
	} {
		if got, err := main.ParseEuroMillicents(in); err != nil || got != want {
			t.Errorf("parseEuroMillicents(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "-1", "+1", "1.000001", "1,5", "abc", "1e3", "99999999"} {
		if _, err := main.ParseEuroMillicents(bad); err == nil {
			t.Errorf("parseEuroMillicents(%q) accepted", bad)
		}
	}
}

func TestParseAIPriceSpec(t *testing.T) {
	entry, err := main.ParseAIPriceSpec("model=gpt-4o-mini,input=0.15,cached=0.075,output=0.60")
	if err != nil || entry.ModelPrefix != "gpt-4o-mini" || entry.InputMillicentsPerMTok != 15_000 ||
		entry.CachedInputMillicentsPerMTok == nil || *entry.CachedInputMillicentsPerMTok != 7_500 ||
		entry.OutputMillicentsPerMTok != 60_000 || entry.CacheWriteMillicentsPerMTok != nil {
		t.Fatalf("price spec = %+v, %v", entry, err)
	}
	for _, bad := range []string{"model=m,input=1", "model=m,input=1,output=2,input=3", "model=m,input=1,output=2,colour=red", "input=1,output=2"} {
		if _, err := main.ParseAIPriceSpec(bad); err == nil {
			t.Errorf("parseAIPriceSpec(%q) accepted", bad)
		}
	}
}

// adr: 991 — guardrail flags merge into the current set; keys come from stdin.
func TestApplyAIGuardrailFlags(t *testing.T) {
	rpm := int64(60)
	current := api.PutOutboundBindingAIGuardrailsRequest{RequestsPerMinute: &rpm, AllowedModels: []string{"gpt-4o"}}
	got, err := main.ApplyAIGuardrailFlags(current, "", "5000", "o3*, gpt-4o-mini", "none")
	if err != nil || got.RequestsPerMinute == nil || *got.RequestsPerMinute != 60 || got.TokensPerMinute == nil || *got.TokensPerMinute != 5000 ||
		len(got.AllowedModels) != 2 || got.AllowedModels[1] != "gpt-4o-mini" || got.MaxOutputTokens != nil || !got.Valid() {
		t.Fatalf("merged guardrails = %+v, %v", got, err)
	}
	cleared, err := main.ApplyAIGuardrailFlags(got, "none", "", "none", "")
	if err != nil || cleared.RequestsPerMinute != nil || cleared.AllowedModels != nil || cleared.TokensPerMinute == nil {
		t.Fatalf("cleared guardrails = %+v, %v", cleared, err)
	}
	if _, err := main.ApplyAIGuardrailFlags(current, "fast", "", "", ""); err == nil {
		t.Fatal("a non-numeric --rpm was accepted")
	}
}

func TestReadCredentialLine(t *testing.T) {
	if got, err := main.ReadCredentialLine(strings.NewReader("aws:AKID:secret\r\nignored\n")); err != nil || got != "aws:AKID:secret" {
		t.Fatalf("line = %q, %v", got, err)
	}
	if _, err := main.ReadCredentialLine(strings.NewReader("")); err == nil {
		t.Fatal("empty stdin accepted")
	}
	if main.AiRouteCallPath(api.OutboundAIRouteFormatOpenAI) != "v1/chat/completions" || main.AiRouteCallPath("native") != "<provider path>" {
		t.Fatal("route call path")
	}
}

func TestCmdAIJSONOutput(t *testing.T) {
	main.ResetJSONOutput()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/outbound/integrations/int-1/ai-usage":
			_ = json.NewEncoder(w).Encode(api.OutboundAIUsageResponse{UsageDate: "2026-10-11", AIProvider: "openai", DailyTokenCount: 42,
				Items: []api.OutboundAIUsageItem{{Caller: "runs", Model: "gpt-4o", Requests: 1, InputTokens: 30, OutputTokens: 12}}})
		case "/v1/outbound/integrations/int-1/ai-alerts":
			_ = json.NewEncoder(w).Encode(api.OutboundAIBudgetAlertList{Items: []api.OutboundAIBudgetAlert{{ID: "a1", Budget: "daily_cost", ThresholdPercent: 80}}})
		case "/v1/outbound/ai-price-catalog":
			_ = json.NewEncoder(w).Encode(api.OutboundAIPriceCatalogResponse{AIProvider: "openai", USDPerEUR: "1.1206",
				Entries: []api.OutboundAIPriceEntry{{ModelPrefix: "gpt-4o", InputMillicentsPerMTok: 223_096, OutputMillicentsPerMTok: 892_380}}})
		case "/v1/outbound/integrations/int-1/ai-usage/users":
			_ = json.NewEncoder(w).Encode(api.OutboundAIUserUsageResponse{UsageDate: "2026-10-12", TrackedUsers: 1,
				Items: []api.OutboundAIUserUsageItem{{EndUser: "user-1", Requests: 2, Tokens: 30, CostMillicents: 5}}})
		case "/v1/outbound/integrations/int-1/credential-keys":
			_ = json.NewEncoder(w).Encode(api.OutboundCredentialKeyList{Items: []api.OutboundCredentialKey{{ID: "k1", Label: "second"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	var buf bytes.Buffer
	prev := (*main.OsStdout)
	(*main.OsStdout) = &buf
	defer func() { (*main.OsStdout) = prev }()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	(*main.JsonOutput) = true
	defer func() { main.ResetJSONOutput() }()
	for _, args := range [][]string{{"usage", "int-1"}, {"alerts", "int-1"}, {"keys", "list", "int-1"}, {"catalog", "openai"}, {"users", "int-1"}} {
		buf.Reset()
		if code := main.CmdAI(args); code != 0 {
			t.Fatalf("gregale ai %v --json = %d", args, code)
		}
		var decoded map[string]any
		if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
			t.Fatalf("gregale ai %v --json is not JSON: %v\n%s", args, err, buf.String())
		}
	}
}

// adr: 995 — `gregale ai users export` writes the server's CSV unchanged
// and asks for the requested range.
func TestCmdAIUsersExport(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/outbound/integrations/int-1/ai-usage/users/export" {
			http.NotFound(w, r)
			return
		}
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/csv")
		_, _ = io.WriteString(w, "usage_date,end_user\n2026-10-12,user-1\n")
	}))
	defer srv.Close()
	var buf bytes.Buffer
	prev := (*main.OsStdout)
	(*main.OsStdout) = &buf
	defer func() { (*main.OsStdout) = prev }()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := main.CmdAI([]string{"users", "export", "int-1", "--from", "2026-10-01"}); code != 0 {
		t.Fatalf("export exit = %d", code)
	}
	if buf.String() != "usage_date,end_user\n2026-10-12,user-1\n" || query != "format=csv&from=2026-10-01" {
		t.Fatalf("export output %q, query %q", buf.String(), query)
	}
	if code := main.CmdAI([]string{"users", "export", "int-1", "--format", "xml"}); code == 0 {
		t.Fatal("an unknown export format was accepted")
	}
}

func TestParseAIRouteTargets(t *testing.T) {
	targets, err := main.ParseAIRouteTargets([]string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222:gpt-4o-mini"})
	if err != nil || len(targets) != 2 || targets[1].Model != "gpt-4o-mini" || targets[0].Model != "" {
		t.Fatalf("targets = %+v, %v", targets, err)
	}
	if _, err := main.ParseAIRouteTargets(nil); err == nil {
		t.Fatal("empty target list accepted")
	}
}

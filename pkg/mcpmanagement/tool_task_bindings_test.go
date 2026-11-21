package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
)

func TestHostedTaskBindingMetadataProtocols(t *testing.T) {
	for _, protocol := range []string{api.MCPProtocolVersion, api.MCPLegacyProtocolVersion} {
		t.Run(protocol, func(t *testing.T) {
			f := newUploadedTaskFixture(t)
			now := time.Now().UTC()
			// The account is obtained from the same native whoami as uploaded reviews.
			var account string
			tamper := false
			var mu sync.Mutex
			reads, writes := 0, 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == "/v1/account" {
					recorder := httptest.NewRecorder()
					f.serve(recorder, r)
					var a api.AccountResponse
					_ = json.Unmarshal(recorder.Body.Bytes(), &a)
					account = a.ID
					for k, v := range recorder.Header() {
						w.Header()[k] = v
					}
					w.WriteHeader(recorder.Code)
					_, _ = w.Write(recorder.Body.Bytes())
					return
				}
				if strings.HasSuffix(r.URL.Path, "/bindings") {
					if r.Method != "GET" {
						writes++
						w.WriteHeader(500)
						return
					}
					reads++
					out := api.HostedTaskBindingVerification{Version: 1, AccountID: account, IntentID: strings.Repeat("a", 64), IntentSHA256: strings.Repeat("b", 64), ReviewSHA256: strings.Repeat("c", 64), BindingSHA256: strings.Repeat("d", 64), Status: "selected", Checks: tasktoolbinding.Results("not_checked"), CreatedAt: now, ExpiresAt: now.Add(time.Minute * 12), OwnerAuthorizationRequired: true}
					if tamper {
						out.BindingsVerified = true
					}
					_ = json.NewEncoder(w).Encode(out)
					return
				}
				f.serve(w, r)
			}))
			defer backend.Close()
			opts := f.opts
			opts.APIBase = backend.URL
			cs, ctx := connectSelectedTestServer(t, backend, opts, protocol)
			defer func() { _ = cs.Close() }()
			args := getUploadedTaskIntentArgs{IntentID: strings.Repeat("a", 64)}
			got := publicationCall(t, ctx, cs, "gregale_get_uploaded_task_tool_bindings", args, false)
			raw, _ := json.Marshal(got)
			for _, s := range []string{"ciphertext", "DATABASE_URL", "secret:", "credential_ref"} {
				if strings.Contains(string(raw), s) {
					t.Fatal("metadata exposed private fields", s)
				}
			}
			mu.Lock()
			tamper = true
			mu.Unlock()
			publicationCall(t, ctx, cs, "gregale_get_uploaded_task_tool_bindings", args, true)
			mu.Lock()
			defer mu.Unlock()
			if reads != 2 || writes != 0 || f.cli.Load() != 0 {
				t.Fatal("metadata getter mutated source or executed CLI", reads, writes)
			}
		})
	}
}

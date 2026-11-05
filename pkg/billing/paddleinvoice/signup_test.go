package paddleinvoice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// adr: 20261022100735 — a Paddle signup reuses the active customer Paddle
// keeps for the email and creates one only when there is none.
func TestPaddleSignupCustomerByEmail(t *testing.T) {
	var requests []string
	var created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Get("email") == "known@example.com":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"ctm_known"}]}`)
		case r.Method == http.MethodGet:
			_, _ = fmt.Fprint(w, `{"data":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/customers":
			_ = json.NewDecoder(r.Body).Decode(&created)
			_, _ = fmt.Fprint(w, `{"data":{"id":"ctm_new"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()
	const key = "pdl_sdbx_apikey_01abcd"

	id, err := c.CustomerByEmail(ctx, key, "known@example.com", "Known")
	if err != nil || id != "ctm_known" || len(requests) != 1 || requests[0] != "GET /customers?status=active&per_page=1&email=known%40example.com" {
		t.Fatalf("existing customer = %q %v %v", id, err, requests)
	}
	id, err = c.CustomerByEmail(ctx, key, "new+tag@example.com", "New workspace")
	if err != nil || id != "ctm_new" || len(requests) != 3 || requests[1] != "GET /customers?status=active&per_page=1&email=new%2Btag%40example.com" ||
		created["email"] != "new+tag@example.com" || created["name"] != "New workspace" {
		t.Fatalf("new customer = %q %v %v %v", id, err, requests, created)
	}
}

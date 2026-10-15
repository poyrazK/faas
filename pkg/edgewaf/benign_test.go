package edgewaf

import (
	"net/http"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

// benignRequest is a legitimate request shaped like real app traffic.
// content marks requests that carry code-like user content (a docs search
// for SQL, HTML from a CMS editor). sampledFP and inPathFP are the CRS rules
// known to flag it at paranoia level 1 off-path and in-path: the false
// positives ADR-831 amendment 6 records, which exclude_rule_ids is for.
type benignRequest struct {
	name                string
	content             bool
	sampledFP, inPathFP []int
	s                   gateway.WAFSample
}

func browserHeaders() http.Header {
	h := http.Header{}
	h.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36")
	h.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	h.Set("Accept-Language", "en-GB,en-US;q=0.9,en;q=0.8,de;q=0.7")
	h.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	h.Set("Sec-Ch-Ua", `"Google Chrome";v="129", "Not=A?Brand";v="8", "Chromium";v="129"`)
	h.Set("Sec-Ch-Ua-Platform", `"macOS"`)
	h.Set("Sec-Fetch-Site", "same-origin")
	h.Set("Sec-Fetch-Mode", "navigate")
	h.Set("Referer", "https://app.example.test/dashboard?tab=overview")
	h.Set("Cookie", "_ga=GA1.1.1234567890.1697040000; _ga_ABC123=GS1.1.1697040000.3.1.1697040123.0.0.0; session=eyJ1aWQiOjQyLCJleHAiOjE3MDAwMDAwMDB9.c2lnbmF0dXJl; theme=dark")
	return h
}

func apiHeaders(ua string) http.Header {
	h := http.Header{}
	h.Set("User-Agent", ua)
	h.Set("Accept", "application/json")
	h.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiIsImtpZCI6IjEifQ.eyJzdWIiOiJ1c2VyXzQyIiwic2NvcGUiOiJyZWFkIHdyaXRlIn0.dGhpc2lzYXNpZ25hdHVyZQ")
	h.Set("X-Request-Id", "9b2f6c1e-3d4a-4e5b-8c7d-1a2b3c4d5e6f")
	return h
}

func benignSample(method, uri string, h http.Header, contentType, body string) gateway.WAFSample {
	s := sample(method, uri, nil, body)
	ua := s.Header.Get("User-Agent")
	for k, v := range h {
		s.Header[k] = v
	}
	if h.Get("User-Agent") == "" {
		s.Header.Set("User-Agent", ua)
	}
	if contentType != "" {
		s.Header.Set("Content-Type", contentType)
	}
	return s
}

func withoutContentType(s gateway.WAFSample) gateway.WAFSample {
	s.Header = s.Header.Clone()
	s.Header.Del("Content-Type")
	return s
}

// benignCorpus is hand-written legitimate traffic, including the shapes
// that commonly trip CRS at paranoia level 1: apostrophes in names, SQL
// keywords in prose, URLs in parameters, tokens in cookies, filter syntax.
func benignCorpus() []benignRequest {
	get := func(uri string, h http.Header) gateway.WAFSample { return benignSample(http.MethodGet, uri, h, "", "") }
	post := func(uri string, h http.Header, ct, body string) gateway.WAFSample {
		return benignSample(http.MethodPost, uri, h, ct, body)
	}
	const json, form = "application/json", "application/x-www-form-urlencoded"
	return []benignRequest{
		{name: "browser page load", s: get("/", browserHeaders())},
		{name: "static asset", s: get("/static/app.3f9a1c.js", browserHeaders())},
		{name: "cache-busting query", s: get("/app.js?v=1.2.3&_=1697040000000", browserHeaders())},
		{name: "REST include list", s: get("/api/v1/users/42?include=profile,settings&fields[user]=name,email", apiHeaders("Go-http-client/1.1"))},
		{name: "search with apostrophe", s: get("/search?q=O%27Brien%27s+pub+%26+grill", browserHeaders())},
		{name: "search with SQL words in prose", s: get("/search?q=select+the+best+from+our+union+jobs+where+you+live", browserHeaders())},
		{name: "OAuth callback", s: get("/auth/callback?code=4%2F0AX4XfWhq9z&state=eyJyZXR1cm4iOiIvZGFzaGJvYXJkIn0&scope=email+profile+openid+https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fuserinfo.email&authuser=0&prompt=consent", browserHeaders())},
		{name: "relative redirect", s: get("/login?next=%2Fdashboard%3Ftab%3Dbilling", browserHeaders())},
		{name: "absolute redirect", s: get("/login?return_to=https%3A%2F%2Fapp.example.test%2Fsettings%2Fprofile", browserHeaders())},
		{name: "filter syntax", s: get("/api/items?filter=price%3E10+and+price%3C100&sort=-created_at&page[size]=50", apiHeaders("python-requests/2.32.3"))},
		{name: "date range", s: get("/reports?from=2026-01-01T00:00:00Z&to=2026-03-31T23:59:59Z&tz=Europe%2FBerlin", apiHeaders("curl/8.5.0"))},
		{name: "unicode search", s: get("/search?q=%E6%9D%B1%E4%BA%AC+caf%C3%A9+%C3%BCber", browserHeaders())},
		{name: "email in query", s: get("/invite?email=jane.doe%2Btest%40example.com&role=admin", browserHeaders())},
		{name: "phone number", s: get("/contact?phone=%2B1+(555)+123-4567&ext=12", browserHeaders())},
		{name: "semicolon id list", s: get("/api/items?ids=1;2;3&expand=true", apiHeaders("okhttp/4.12.0"))},
		{name: "GraphQL GET", sampledFP: []int{942190}, inPathFP: []int{942190}, s: get("/graphql?query=%7Buser(id%3A%2242%22)%7Bname+email+orders(first%3A10)%7Bid+total%7D%7D%7D", apiHeaders("Apollo/3.8"))},
		{name: "markdown preview", s: get("/preview?text=**bold**+and+_italic_+%5Blink%5D(https%3A%2F%2Fexample.com)", browserHeaders())},
		{name: "Stripe webhook", s: post("/webhooks/stripe", http.Header{"User-Agent": {"Stripe/1.0 (+https://stripe.com/docs/webhooks)"}, "Stripe-Signature": {"t=1697040000,v1=5257a869e7ecebeda32affa62cdca3fa51cad7e77a0e56ff536d0ce8e108d8bd"}}, json,
			`{"id":"evt_1NG8Du2eZvKYlo2CUI79vXWy","type":"invoice.paid","data":{"object":{"id":"in_1NG8","amount_paid":2000,"currency":"eur","customer_email":"jane@example.com","description":"Pro plan (monthly)"}}}`)},
		{name: "GitHub webhook", s: post("/hooks/github", http.Header{"User-Agent": {"GitHub-Hookshot/7a1b2c3"}, "X-Github-Event": {"push"}}, json,
			`{"ref":"refs/heads/main","head_commit":{"message":"fix: don't drop the user's last order when the cart is empty\n\nCloses #42","author":{"name":"Jane O'Neil"}}}`)},
		{name: "JSON order", s: post("/api/orders?page=2&sort=created", apiHeaders("Go-http-client/1.1"), json, jsonBody(2048, ""))},
		{name: "JSON comment with prose", sampledFP: []int{942350}, s: post("/api/comments", apiHeaders("Mozilla/5.0"), json,
			`{"comment":"I can't believe it's not butter; select your favourite from the list & enjoy! Price < 10 EUR -- best deal","rating":5}`)},
		{name: "login form", s: post("/login", browserHeaders(), form, "username=jane.doe%40example.com&password=Tr0ub4dor%263&remember=on")},
		{name: "address form", s: post("/checkout", browserHeaders(), form, "name=Jane+O%27Neil&address=12+Main+St.%2C+Apt+%234&city=Dublin&notes=Leave+at+door+-+don%27t+ring")},
		{name: "GraphQL POST", sampledFP: []int{932235, 942290}, s: post("/graphql", apiHeaders("Apollo/3.8"), json,
			`{"query":"query Orders($first: Int!) { viewer { orders(first: $first, orderBy: {field: CREATED_AT, direction: DESC}) { edges { node { id total status } } } } }","variables":{"first":10}}`)},
		{name: "JSON with URLs", s: post("/api/profile", apiHeaders("Mozilla/5.0"), json,
			`{"website":"https://jane.example.com/about?ref=profile","avatar_url":"https://cdn.example.com/u/42/avatar.png","callback_url":"https://hooks.example.com/notify?id=1&sig=abc"}`)},
		{name: "plain text log", s: post("/ingest", apiHeaders("vector/0.40.0"), "text/plain",
			"2026-10-14T09:12:01Z INFO request completed method=GET path=/api/items status=200 duration_ms=12\n2026-10-14T09:12:02Z WARN slow query took 812ms\n")},
		{name: "NDJSON batch", s: post("/api/events/batch", apiHeaders("Go-http-client/1.1"), "application/x-ndjson",
			`{"event":"signup","user":42}`+"\n"+`{"event":"login","user":42}`+"\n")},
		{name: "binary upload", s: benignSample(http.MethodPut, "/api/files/report.pdf", apiHeaders("Go-http-client/1.1"), "application/octet-stream", "%PDF-1.7\n%\xe2\xe3\xcf\xd3\n1 0 obj\n<< /Type /Catalog >>\nendobj\n")},
		{name: "REST PUT", s: benignSample(http.MethodPut, "/api/v1/users/42", apiHeaders("Go-http-client/1.1"), json, `{"name":"Jane","email":"jane@example.com"}`)},
		{name: "REST PATCH", s: benignSample(http.MethodPatch, "/api/v1/users/42", apiHeaders("Go-http-client/1.1"), "application/merge-patch+json", `{"email":"jane.doe@example.com"}`)},
		{name: "REST DELETE", s: benignSample(http.MethodDelete, "/api/v1/sessions/9b2f6c1e", apiHeaders("Go-http-client/1.1"), "", "")},
		// requests.post(url, data=json.dumps(order)) sends no Content-Type.
		{name: "body without Content-Type", s: withoutContentType(post("/api/orders", apiHeaders("python-requests/2.32.3"), json, `{"sku":"SKU-000042","qty":2}`))},

		{name: "docs search for SQL", content: true, sampledFP: []int{942100}, inPathFP: []int{942100}, s: get("/search?q=SELECT+*+FROM+users+WHERE+id+%3D+1", browserHeaders())},
		{name: "docs search for shell", content: true, s: get("/search?q=rm+-rf+node_modules+%26%26+npm+install", browserHeaders())},
		{name: "Windows path in query", content: true, s: get("/upload?path=C%3A%5CUsers%5Cjane%5CDocuments%5Creport.docx", browserHeaders())},
		{name: "HTML search", content: true, s: get("/docs?q=how+to+use+%3Cdiv%3E+and+%3Cspan%3E+tags", browserHeaders())},
		{name: "CMS rich text", content: true, sampledFP: []int{941160}, s: post("/api/posts", apiHeaders("Mozilla/5.0"), json,
			`{"title":"Release notes","body":"<p>Hello <strong>world</strong>, see <a href=\"https://example.com/docs\" target=\"_blank\">the docs</a>.</p><img src=\"/img/chart.png\" alt=\"chart\">"}`)},
		{name: "code snippet in JSON", content: true, sampledFP: []int{942100}, s: post("/api/snippets", apiHeaders("Mozilla/5.0"), json,
			`{"language":"sql","code":"SELECT id, email FROM users WHERE created_at > now() - interval '7 days' ORDER BY id;"}`)},
	}
}

// TestBenignCorpus checks which legitimate requests each configuration
// flags. At paranoia level 1 the flagged requests and their rules must be
// exactly the known false positives, so a CRS upgrade that adds or removes
// one fails here and the ADR's table is updated with it. Paranoia level 2
// is reported only.
func TestBenignCorpus(t *testing.T) {
	inline, err := compileInline()
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []struct {
		name   string
		pl     int
		inPath bool
	}{{"sampled PL1", 1, false}, {"sampled PL2", 2, false}, {"in-path PL1", 1, true}} {
		waf := inline
		if !cfg.inPath {
			if waf, err = compile(cfg.pl); err != nil {
				t.Fatal(err)
			}
		}
		flagged := 0
		for _, br := range benignCorpus() {
			s := br.s
			s.ParanoiaLevel = cfg.pl
			want := br.sampledFP
			if cfg.inPath {
				s.Body, want = nil, br.inPathFP
			}
			res, err := evaluate(waf, s)
			if err != nil {
				t.Fatalf("%s %s: %v", cfg.name, br.name, err)
			}
			if res.Detected {
				flagged++
				t.Logf("[%s] flagged %q (content=%v): score %d, rules %v", cfg.name, br.name, br.content, res.Score, res.RuleIDs)
			}
			if cfg.pl != 1 {
				continue
			}
			var got []int
			if res.Detected {
				got = res.RuleIDs
			}
			if !slices.Equal(got, want) {
				t.Errorf("[%s] %q flagged by %v (score %d), want known false positives %v", cfg.name, br.name, got, res.Score, want)
			}
		}
		t.Logf("[%s] %d of %d benign requests flagged", cfg.name, flagged, len(benignCorpus()))
	}
}

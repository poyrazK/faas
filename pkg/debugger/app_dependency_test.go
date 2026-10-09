package debugger

// adr: 829 — standard OTel client spans become bounded app_dependency identities.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClassifyAppDependency(t *testing.T) {
	for _, tc := range []struct {
		name     string
		span     StoredSpan
		wantKind string
		wantName string
		wantOK   bool
	}{
		{name: "postgres operation and table attrs",
			span:     StoredSpan{Kind: "SPAN_KIND_CLIENT", Attributes: map[string]string{"db.system.name": "postgresql", "db.operation.name": "select", "db.collection.name": "orders"}},
			wantKind: "postgresql", wantName: "SELECT orders", wantOK: true},
		{name: "legacy db.system with statement fallback, literals dropped",
			span:     StoredSpan{Kind: "SPAN_KIND_CLIENT", DBStatement: "SELECT * FROM public.orders WHERE email = 'a@b.c' AND id = 42", Attributes: map[string]string{"db.system": "postgres"}},
			wantKind: "postgresql", wantName: "SELECT public.orders", wantOK: true},
		{name: "insert into quoted table",
			span:     StoredSpan{Kind: "client", DBStatement: `INSERT INTO "line_items" (id) VALUES ($1)`, Attributes: map[string]string{"db.system": "postgresql"}},
			wantKind: "postgresql", wantName: "INSERT line_items", wantOK: true},
		{name: "redis command only, never the key",
			span:     StoredSpan{Kind: "SPAN_KIND_CLIENT", DBStatement: "GET session:secret-token-123", Attributes: map[string]string{"db.system": "redis"}},
			wantKind: "redis", wantName: "GET", wantOK: true},
		{name: "db span without client kind still classified",
			span:     StoredSpan{Kind: "SPAN_KIND_INTERNAL", Attributes: map[string]string{"db.system": "mongodb", "db.operation": "find", "db.mongodb.collection": "users"}},
			wantKind: "mongodb", wantName: "FIND users", wantOK: true},
		{name: "http client host from server.address",
			span:     StoredSpan{Kind: "SPAN_KIND_CLIENT", Name: "GET", Attributes: map[string]string{"http.request.method": "GET", "server.address": "API.Stripe.com", "server.port": "443"}},
			wantKind: "http", wantName: "api.stripe.com", wantOK: true},
		{name: "http client host from url, no path/query/userinfo",
			span:     StoredSpan{Kind: "SPAN_KIND_CLIENT", Attributes: map[string]string{"http.method": "POST", "http.url": "https://user:pw@hooks.example.com:8443/v1/charge?key=sk_live"}},
			wantKind: "http", wantName: "hooks.example.com", wantOK: true},
		{name: "http server span ignored",
			span:   StoredSpan{Kind: "SPAN_KIND_SERVER", Attributes: map[string]string{"http.request.method": "GET", "server.address": "app.example"}},
			wantOK: false},
		{name: "grpc",
			span:     StoredSpan{Kind: "SPAN_KIND_CLIENT", Attributes: map[string]string{"rpc.system": "grpc", "rpc.service": "billing.Ledger", "rpc.method": "Post"}},
			wantKind: "grpc", wantName: "billing.Ledger/Post", wantOK: true},
		{name: "kafka producer",
			span:     StoredSpan{Kind: "SPAN_KIND_PRODUCER", Attributes: map[string]string{"messaging.system": "kafka", "messaging.operation.type": "publish", "messaging.destination.name": "orders"}},
			wantKind: "kafka", wantName: "publish orders", wantOK: true},
		{name: "plain internal span",
			span:   StoredSpan{Kind: "SPAN_KIND_INTERNAL", Name: "render"},
			wantOK: false},
		{name: "hostile system name bounded",
			span:     StoredSpan{Kind: "SPAN_KIND_CLIENT", Attributes: map[string]string{"db.system": "Weird System!", "db.operation": "x"}},
			wantKind: "other", wantName: "X", wantOK: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind, name, ok := classifyAppDependency(tc.span)
			if ok != tc.wantOK || kind != tc.wantKind || name != tc.wantName {
				t.Fatalf("classify = (%q, %q, %v), want (%q, %q, %v)", kind, name, ok, tc.wantKind, tc.wantName, tc.wantOK)
			}
		})
	}
}

func TestParseSpansClassifiesAppDependencies(t *testing.T) {
	raw, _ := json.Marshal([]StoredSpan{
		{SpanID: "a", Name: "SELECT orders", Kind: "SPAN_KIND_CLIENT", DurationNanos: 191_000_000,
			DBStatement: "SELECT * FROM orders WHERE id = 7", Attributes: map[string]string{"db.system": "postgresql"}},
		// A platform-owned classification wins over semantic conventions.
		{SpanID: "b", Name: "neon", Kind: "SPAN_KIND_CLIENT", DurationNanos: 5_000_000,
			Attributes: map[string]string{"gregale.dependency.type": "managed_binding", "gregale.dependency.kind": "postgres", "db.system": "postgresql"}},
		{SpanID: "c", Name: "render", Kind: "SPAN_KIND_INTERNAL", DurationNanos: 1_000_000},
	})
	spans, _ := ParseSpans(raw)
	if len(spans) != 3 {
		t.Fatalf("spans = %d", len(spans))
	}
	byID := map[string]int{}
	for i, span := range spans {
		byID[span.SpanID] = i
	}
	app := spans[byID["a"]]
	if app.DependencyType != AppDependencyType || app.DependencyKind != "postgresql" || app.DependencyName != "SELECT orders" {
		t.Fatalf("app span = %+v", app)
	}
	if SegmentName(app) != "SELECT orders" {
		t.Fatalf("segment name = %q", SegmentName(app))
	}
	platform := spans[byID["b"]]
	if platform.DependencyType != "managed_binding" || platform.DependencyName != "" {
		t.Fatalf("platform span = %+v", platform)
	}
	internal := spans[byID["c"]]
	if internal.DependencyType != "" || SegmentName(internal) != "render" {
		t.Fatalf("internal span = %+v", internal)
	}
	for _, span := range spans {
		if strings.Contains(span.DependencyName, "7") {
			t.Fatalf("literal leaked into dependency name: %q", span.DependencyName)
		}
	}
}

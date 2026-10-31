package gregalemanifest

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseRoutes(t *testing.T) {
	m, err := ParseBytes([]byte(`
routes:
  - name: api
    match_host: shop.example.com
    match_path: /api/**
    to: api
    strip_prefix: /api
    match_methods: [post, GET]
    priority: 50
  - name: web
    match_host: shop.example.com
    match_path: /**
    to: web
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(m.Routes) != 2 || m.Routes[0].To != "api" || m.Routes[0].StripPrefix != "/api" || *m.Routes[0].Priority != 50 {
		t.Fatalf("routes = %+v", m.Routes)
	}
	if got := m.Routes[0].Methods(); !reflect.DeepEqual(got, []string{"GET", "POST"}) {
		t.Fatalf("methods = %v", got)
	}
	if _, err := ParseBytes([]byte("routes:\n  - name: api\n    match_host: a.example.com\n    match_path: /x\n    target: api\n")); err == nil {
		t.Fatal("unknown route field accepted")
	}
	empty, err := ParseBytes([]byte("routes: []\n"))
	if err != nil || empty.Routes == nil || len(empty.Routes) != 0 {
		t.Fatalf("explicit empty list must stay non-nil: %+v %v", empty, err)
	}
}

func TestValidateRoutes(t *testing.T) {
	valid := PathRoute{Name: "api", MatchHost: "shop.example.com", MatchPath: "/api/**", To: "api", StripPrefix: "/api"}
	priority := 20000
	cases := map[string]struct {
		route PathRoute
		want  string
	}{
		"name":          {PathRoute{Name: "API", MatchHost: "h", MatchPath: "/", To: "api"}, "routes[0].name"},
		"to":            {PathRoute{Name: "a", MatchHost: "h", MatchPath: "/", To: ""}, "routes[0].to"},
		"host":          {PathRoute{Name: "a", MatchHost: "https://h/x", MatchPath: "/", To: "api"}, "match_host"},
		"path":          {PathRoute{Name: "a", MatchHost: "h", MatchPath: "api", To: "api"}, "match_path"},
		"template":      {PathRoute{Name: "a", MatchHost: "h", MatchPath: "/items/{id}", To: "api"}, "OpenAPI template"},
		"strip root":    {PathRoute{Name: "a", MatchHost: "h", MatchPath: "/api/**", To: "api", StripPrefix: "/"}, "strip_prefix"},
		"strip slash":   {PathRoute{Name: "a", MatchHost: "h", MatchPath: "/api/**", To: "api", StripPrefix: "/api/"}, "strip_prefix"},
		"strip glob":    {PathRoute{Name: "a", MatchHost: "h", MatchPath: "/api/**", To: "api", StripPrefix: "/api*"}, "strip_prefix"},
		"strip outside": {PathRoute{Name: "a", MatchHost: "h", MatchPath: "/v2/**", To: "api", StripPrefix: "/api"}, "must be a prefix"},
		"priority":      {PathRoute{Name: "a", MatchHost: "h", MatchPath: "/", To: "api", Priority: &priority}, "priority"},
		"method":        {PathRoute{Name: "a", MatchHost: "h", MatchPath: "/", To: "api", MatchMethods: []string{"TRACE"}}, "match_methods"},
		"dup method":    {PathRoute{Name: "a", MatchHost: "h", MatchPath: "/", To: "api", MatchMethods: []string{"GET", "get"}}, "duplicate method"},
	}
	if err := valid.Validate(0); err != nil {
		t.Fatalf("valid route: %v", err)
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := tc.route.Validate(0); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	dupName := (&Manifest{Routes: []PathRoute{valid, {Name: "api", MatchHost: "other.example.com", MatchPath: "/", To: "api"}}}).Validate()
	if dupName == nil || !strings.Contains(dupName.Error(), "duplicate name") {
		t.Fatalf("duplicate name: %v", dupName)
	}
	dupMatch := (&Manifest{Routes: []PathRoute{valid, {Name: "api-two", MatchHost: "SHOP.example.com", MatchPath: "/api/**", To: "other"}}}).Validate()
	if dupMatch == nil || !strings.Contains(dupMatch.Error(), "same match_host") {
		t.Fatalf("duplicate match: %v", dupMatch)
	}
	sameNameOtherApp := (&Manifest{Routes: []PathRoute{valid, {Name: "api", MatchHost: "shop.example.com", MatchPath: "/**", To: "web"}}}).Validate()
	if sameNameOtherApp != nil {
		t.Fatalf("names are per target app: %v", sameNameOtherApp)
	}
}

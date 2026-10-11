package httpsec

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestClassifyHost(t *testing.T) {
	for host, want := range map[string]Surface{
		"gregale.dev":            SurfacePlatform,
		"api.gregale.dev":        SurfacePlatform,
		"operations.gregale.dev": SurfacePlatform,
		"API.Gregale.dev.:443":   SurfacePlatform,
		"localhost:8080":         SurfacePlatform,
		"127.0.0.1":              SurfacePlatform,
		"[::1]:8080":             SurfacePlatform,
		"shop.gregale.dev":       SurfaceAppHost,
		"pr-7.shop.gregale.dev":  SurfaceAppHost,
		"example.com":            SurfaceCustomDomain,
		"api.example.com:443":    SurfaceCustomDomain,
		"notgregale.dev":         SurfaceCustomDomain,
	} {
		if got := ClassifyHost(host, "gregale.dev"); got != want {
			t.Errorf("ClassifyHost(%q) = %v, want %v", host, got, want)
		}
	}
	if got := ClassifyHost("example.com", ""); got != SurfacePlatform {
		t.Errorf("no apps domain: %v, want platform (pre-ADR-830 forced headers)", got)
	}
}

func TestForSurfaceHeaders(t *testing.T) {
	SetHSTSEnabled(true)
	classify := func(r *http.Request) Surface { return ClassifyHost(r.Host, "gregale.dev") }
	var seen Surface
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = SurfaceFrom(r.Context()) })
	forced := http.Header{}
	setStatic(forced)

	for _, tc := range []struct {
		host         string
		fillDefaults bool
		want         http.Header
		surface      Surface
	}{
		{"api.gregale.dev", true, forced, SurfacePlatform},
		{"api.gregale.dev", false, forced, SurfacePlatform},
		{"shop.gregale.dev", true, http.Header{
			HeaderStrictTransportSecurity: {ValueHSTSMaxAge},
			HeaderXContentTypeOptions:     {ValueXContentTypeOptions},
			HeaderReferrerPolicy:          {ValueReferrerPolicy},
		}, SurfaceAppHost},
		{"example.com", true, http.Header{
			HeaderStrictTransportSecurity: {ValueHSTSCustomDomain},
			HeaderXContentTypeOptions:     {ValueXContentTypeOptions},
			HeaderReferrerPolicy:          {ValueReferrerPolicy},
		}, SurfaceCustomDomain},
		{"example.com", false, http.Header{}, SurfaceCustomDomain},
	} {
		rec := httptest.NewRecorder()
		ForSurface(classify, tc.fillDefaults, next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/", nil))
		if !reflect.DeepEqual(rec.Header(), tc.want) {
			t.Errorf("%s fill=%v headers = %v, want %v", tc.host, tc.fillDefaults, rec.Header(), tc.want)
		}
		if seen != tc.surface {
			t.Errorf("%s recorded surface %v, want %v", tc.host, seen, tc.surface)
		}
	}
}

func TestCopyCustomerStaticHeader(t *testing.T) {
	dst := http.Header{HeaderStrictTransportSecurity: {ValueHSTSCustomDomain}}
	replaced := map[string]bool{}
	CopyCustomerStaticHeader(dst, "strict-transport-security", []string{"max-age=63072000; preload"}, replaced)
	CopyCustomerStaticHeader(dst, HeaderXFrameOptions, []string{"SAMEORIGIN"}, replaced)
	want := http.Header{
		HeaderStrictTransportSecurity: {"max-age=63072000; preload"},
		HeaderXFrameOptions:           {"SAMEORIGIN"},
	}
	if !reflect.DeepEqual(dst, want) {
		t.Fatalf("headers = %v, want the app values replacing defaults", dst)
	}
	if SurfaceFrom(httptest.NewRequest(http.MethodGet, "/", nil).Context()) != SurfacePlatform {
		t.Fatal("unclassified request must default to the forced platform policy")
	}
}

package api

import (
	"strings"
	"testing"
)

func TestPublicBuildURLBoundary(t *testing.T) {
	for _, key := range []string{"VITE_API_URL", "NEXT_PUBLIC_API_URL", "PUBLIC_BACKEND_URL", "NUXT_PUBLIC_API_URL", "GATSBY_API_URL", "REACT_APP_API_URL"} {
		if ValidatePublicBuildURLKey(key) != nil {
			t.Fatalf("valid frontend URL key rejected: %s", key)
		}
	}
	for _, key := range []string{"API_URL", "DATABASE_URL", "PORT", "GREGALE_PUBLIC_URL", "VITE_TOKEN", "VITE_API_URL\nTOKEN"} {
		if ValidatePublicBuildURLKey(key) == nil {
			t.Fatalf("nonpublic URL key accepted: %s", key)
		}
	}
	for _, value := range []string{"https://api.example.com", "https://api.example.com/v1/", "https://api.example.com:443/v1"} {
		if ValidatePublicBuildURL(value) != nil {
			t.Fatalf("public URL rejected: %s", value)
		}
	}
	for _, value := range []string{"http://api.example.com", "https://user:private@api.example.com", "https://api.example.com?token=private", "https://api.example.com#private", "https://api.example.com?", "https://localhost", "https://api.internal", "https://127.0.0.1", "https://[::1]", "https://api.local", "https://api.example.com:8443", " https://api.example.com", "https://api.example.com/\nprivate", "https://api.example.com/" + strings.Repeat("x", PublicBuildURLMaxBytes)} {
		if ValidatePublicBuildURL(value) == nil {
			t.Fatal("unsafe public URL accepted")
		}
	}
	a := map[string]string{"VITE_API_URL": "https://api.example.com", "VITE_AUTH_URL": "https://auth.example.com"}
	b := map[string]string{"VITE_AUTH_URL": a["VITE_AUTH_URL"], "VITE_API_URL": a["VITE_API_URL"]}
	if PublicBuildURLsSHA256(a) != PublicBuildURLsSHA256(b) || PublicBuildURLsSHA256(nil) != "" {
		t.Fatal("non-deterministic public build identity")
	}
	b["VITE_API_URL"] = "https://new.example.com"
	if PublicBuildURLsSHA256(a) == PublicBuildURLsSHA256(b) {
		t.Fatal("changed public URL retained build identity")
	}
}

func TestPublicBuildURLConfigurationLimits(t *testing.T) {
	urls := map[string]string{}
	for i := range PublicBuildURLsMaxPerApp + 1 {
		urls["VITE_API_"+string(rune('A'+i))+"_URL"] = "https://api.example.com"
	}
	if ValidatePublicBuildURLs(urls) == nil {
		t.Fatal("public URL count limit bypassed")
	}
	delete(urls, "VITE_API_K_URL")
	if problem := ValidatePublicBuildURLs(urls); problem != nil {
		t.Fatal(problem)
	}
	for key := range urls {
		urls[key] = "https://api.example.com/" + strings.Repeat("&", 1800)
	}
	if ValidatePublicBuildURLs(urls) == nil {
		t.Fatal("encoded public URL limit bypassed")
	}
}

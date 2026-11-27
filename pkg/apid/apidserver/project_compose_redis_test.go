package apidserver_test

import (
	"regexp"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

var cacheSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

func TestComposeRedisCacheName(t *testing.T) {
	for _, tc := range []struct {
		project, service, scope, want string
	}{
		{"shop", "redis", "default", "shop-redis"},
		{"shop", "redis-cart", "default", "shop-redis-cart"},
	} {
		if got := apidserver.ComposeRedisCacheName(tc.project, tc.service, tc.scope); got != tc.want {
			t.Errorf("%s/%s/%s = %q, want %q", tc.project, tc.service, tc.scope, got, tc.want)
		}
	}
	long := apidserver.ComposeRedisCacheName("a-very-long-project-name-for-tests", "session-cache", "default")
	staging := apidserver.ComposeRedisCacheName("shop", "redis", "staging")
	other := apidserver.ComposeRedisCacheName("shop", "redis", "preview-1")
	for _, name := range []string{long, staging, other} {
		if !cacheSlug.MatchString(name) {
			t.Errorf("%q is not a valid cache name", name)
		}
	}
	if staging == other || staging == "shop-redis" {
		t.Fatalf("stages must not share a cache: %q %q", staging, other)
	}
}

func TestComposeRedisValueKeepsShape(t *testing.T) {
	ptr := func(s string) *string { return &s }
	for _, tc := range []struct {
		in   *string
		want string
	}{
		{nil, "redis://shop-redis.svc.gregale:6379"},
		{ptr("redis:6379"), "shop-redis.svc.gregale:6379"},
		{ptr("redis"), "shop-redis.svc.gregale"},
		{ptr("redis://redis:6379/2"), "redis://shop-redis.svc.gregale:6379/2"},
		{ptr("rediss://user:secret@redis:6380/0?timeout=5s"), "redis://shop-redis.svc.gregale:6379/0?timeout=5s"},
	} {
		in := "<nil>"
		if tc.in != nil {
			in = *tc.in
		}
		if got := apidserver.ComposeRedisValue(tc.in, "shop-redis"); got != tc.want {
			t.Errorf("%s -> %q, want %q", in, got, tc.want)
		}
	}
}

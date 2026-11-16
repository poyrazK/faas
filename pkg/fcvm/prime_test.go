package fcvm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClampPrimeOptions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     PrimeOptions
		want   PrimeOptions
		errNil bool
	}{
		{name: "defaults", in: PrimeOptions{Path: "/healthz"}, want: PrimeOptions{Path: "/healthz", Requests: PrimeMaxRequests, Budget: PrimeMaxBudget}, errNil: true},
		{name: "kept inside limits", in: PrimeOptions{Path: "/", Requests: 3, Budget: time.Second}, want: PrimeOptions{Path: "/", Requests: 3, Budget: time.Second}, errNil: true},
		{name: "capped", in: PrimeOptions{Path: "/x", Requests: 500, Budget: time.Minute}, want: PrimeOptions{Path: "/x", Requests: PrimeMaxRequests, Budget: PrimeMaxBudget}, errNil: true},
		{name: "relative path", in: PrimeOptions{Path: "healthz"}},
		{name: "absolute URL", in: PrimeOptions{Path: "http://169.254.169.254/"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := clampPrimeOptions(tc.in)
			if !tc.errNil {
				if !errors.Is(err, ErrPrimeInvalidPath) {
					t.Fatalf("err = %v, want ErrPrimeInvalidPath", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("clamp = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestPrimeHTTP(t *testing.T) {
	t.Run("sends every request", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/readyz" {
				t.Errorf("path = %q", r.URL.Path)
			}
			hits.Add(1)
		}))
		defer srv.Close()
		rep := primeHTTP(context.Background(), strings.TrimPrefix(srv.URL, "http://"), PrimeOptions{Path: "/readyz", Requests: 20, Budget: 5 * time.Second})
		if rep.Sent != 20 || rep.OK != 20 || hits.Load() != 20 {
			t.Fatalf("report = %+v, hits %d; want 20/20", rep, hits.Load())
		}
	})
	t.Run("non-2xx counts as sent", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		rep := primeHTTP(context.Background(), strings.TrimPrefix(srv.URL, "http://"), PrimeOptions{Path: "/", Requests: 4, Budget: 5 * time.Second})
		if rep.Sent != 4 || rep.OK != 0 {
			t.Fatalf("report = %+v, want 4 sent, 0 ok", rep)
		}
	})
	t.Run("redirect is not followed", func(t *testing.T) {
		var followed atomic.Bool
		other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Store(true) }))
		defer other.Close()
		srv := httptest.NewServer(http.RedirectHandler(other.URL, http.StatusFound))
		defer srv.Close()
		primeHTTP(context.Background(), strings.TrimPrefix(srv.URL, "http://"), PrimeOptions{Path: "/", Requests: 2, Budget: 5 * time.Second})
		if followed.Load() {
			t.Fatal("priming followed a redirect off the guest")
		}
	})
	t.Run("budget cuts a slow app short", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			time.Sleep(60 * time.Millisecond)
		}))
		defer srv.Close()
		started := time.Now()
		rep := primeHTTP(context.Background(), strings.TrimPrefix(srv.URL, "http://"), PrimeOptions{Path: "/", Requests: 20, Budget: 150 * time.Millisecond})
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("burst took %s, want the budget to stop it", elapsed)
		}
		if rep.Sent >= 20 || rep.OK != rep.Sent {
			t.Fatalf("report = %+v, want a short burst of completed requests", rep)
		}
	})
	t.Run("nothing listening", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		addr := strings.TrimPrefix(srv.URL, "http://")
		srv.Close()
		rep := primeHTTP(context.Background(), addr, PrimeOptions{Path: "/", Requests: 3, Budget: 2 * time.Second})
		if rep.OK != 0 {
			t.Fatalf("report = %+v, want no 2xx", rep)
		}
	})
}

package httpkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPrincipalFrom(t *testing.T) {
	acct := state.Account{ID: uuid.NewString()}
	key := &state.APIKey{ID: uuid.NewString()}
	tests := []struct {
		name    string
		ctx     func(context.Context) context.Context
		wantOK  bool
		wantKey *state.APIKey
	}{
		{name: "no principal", ctx: func(c context.Context) context.Context { return c }},
		{
			name:   "session principal",
			ctx:    func(c context.Context) context.Context { return authmw.WithPrincipal(c, acct, nil, nil) },
			wantOK: true,
		},
		{
			name:    "api key principal",
			ctx:     func(c context.Context) context.Context { return authmw.WithPrincipal(c, acct, key, nil) },
			wantOK:  true,
			wantKey: key,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r = r.WithContext(tt.ctx(r.Context()))
			p, ok := PrincipalFrom(r)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if p.Acct.ID != acct.ID || p.Key != tt.wantKey {
				t.Fatalf("principal = %+v", p)
			}
		})
	}
}

func TestAccountFrom(t *testing.T) {
	if _, ok := AccountFrom(context.Background()); ok {
		t.Fatal("AccountFrom on empty context returned ok")
	}
	acct := state.Account{ID: uuid.NewString()}
	got, ok := AccountFrom(WithAccount(context.Background(), acct))
	if !ok || got.ID != acct.ID {
		t.Fatalf("AccountFrom = %+v, %v", got, ok)
	}
}

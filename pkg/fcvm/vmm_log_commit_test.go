package fcvm

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestJailerVMM_RegisterAppRingObservesAppLinesOnly(t *testing.T) {
	t.Parallel()
	v := NewJailerVMM("/srv/fc", 0)
	seen := map[string]int{}
	v.WithLogCommitCallback(func(appID string, _ logbuf.Line) { seen[appID]++ })
	appCtx := wire.WithContext(context.Background(), wire.CorrelationFields{AppID: "app-1"})

	tests := []struct {
		name  string
		ctx   context.Context
		lease Lease
	}{
		{"app VM", appCtx, Lease{Instance: "i-app"}},
		{"builder VM", appCtx, Lease{Instance: "i-build", IsBuilder: true}},
		{"no app id", context.Background(), Lease{Instance: "i-anon"}},
	}
	for _, tt := range tests {
		r := v.registerAppRing(tt.ctx, tt.lease)
		if _, err := r.Write("stdout", []byte("[error] boom\n")); err != nil {
			t.Fatalf("%s: write: %v", tt.name, err)
		}
		v.unregisterRing(tt.lease.Instance)
	}
	if len(seen) != 1 || seen["app-1"] != 1 {
		t.Fatalf("observed %v; want exactly one line for app-1", seen)
	}
}

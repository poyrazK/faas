package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBindingPromotionGraphGuardsEveryCatalogMemberTogether(t *testing.T) {
	ctx := context.Background()
	catalog := NewMemoryStore()
	service := &BindingService{bindings: catalog}
	fences := []AppPromotionFence{}
	for _, app := range []string{"api", "billing"} {
		fence, err := service.ReadPromotionFence(ctx, "account", app)
		if err != nil {
			t.Fatal(err)
		}
		fences = append(fences, AppPromotionFence{AppID: app, Fence: fence})
	}
	invalid := append([]AppPromotionFence(nil), fences...)
	invalid[1].Fence.backend = NewMemoryStore()
	called := false
	if err := service.GuardPromotions(ctx, "account", invalid, nil, func(context.Context) error { called = true; return nil }); !errors.Is(err, ErrConflict) || called {
		t.Fatalf("second catalog bypassed: called=%t err=%v", called, err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- service.GuardPromotions(ctx, "account", fences, nil, func(context.Context) error { close(entered); <-release; return nil })
	}()
	<-entered
	read := make(chan struct{})
	go func() { _, _ = service.ReadPromotionFence(ctx, "account", "billing"); close(read) }()
	select {
	case <-read:
		t.Error("catalog changed before graph callback finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	<-read
}

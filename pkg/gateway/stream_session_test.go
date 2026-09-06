package gateway

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

func TestNewStreamSessionDetachesRequestBudget(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()
	budgetCtx, budgetCancel, _ := reqbudget.WithRemaining(parent, 100*time.Millisecond, 100*time.Millisecond, "forward", "GET:/stream")
	defer budgetCancel()

	ctx, detach, _, cancel := newStreamSession(budgetCtx, time.Second, time.Second)
	defer cancel()
	detach()

	select {
	case <-ctx.Done():
		t.Fatal("stream session was canceled by the detached request budget")
	case <-time.After(150 * time.Millisecond):
	}

	parentCancel()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("stream session error = %v, want canceled", ctx.Err())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("stream session did not preserve parent cancellation")
	}
}

func TestNewStreamSessionIdleTimeoutResetsOnActivity(t *testing.T) {
	ctx, _, touch, cancel := newStreamSession(context.Background(), 0, 100*time.Millisecond)
	defer cancel()

	for i := 0; i < 3; i++ {
		time.Sleep(40 * time.Millisecond)
		touch()
		select {
		case <-ctx.Done():
			t.Fatal("active stream was canceled before its idle timeout")
		default:
		}
	}

	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("idle stream error = %v, want canceled", ctx.Err())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("idle stream did not close after its quiet-period timeout")
	}
}

func TestIsLongLivedResponse(t *testing.T) {
	for _, tc := range []struct {
		statusCode int
		status     string
		want       bool
	}{
		{http.StatusOK, string(api.StreamingStatusStreaming), true},
		{http.StatusSwitchingProtocols, string(api.StreamingStatusUpgradeBypass), true},
		{http.StatusOK, string(api.StreamingStatusAcceptJSONDowngrade), false},
		{http.StatusBadGateway, string(api.StreamingStatusStreaming), false},
		{http.StatusOK, "", false},
	} {
		h := make(http.Header)
		h.Set(api.StreamingStatusHeader, tc.status)
		if got := isLongLivedResponse(tc.statusCode, h); got != tc.want {
			t.Errorf("status %q: isLongLivedResponse = %v, want %v", tc.status, got, tc.want)
		}
	}
}

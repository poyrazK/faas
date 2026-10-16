package apidserver

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWriteDeploymentCreateError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "inherited controls pending",
			err:        errors.Join(errors.New("create deployment"), state.ErrApplicationStandardsPending),
			wantStatus: http.StatusConflict,
			wantCode:   api.CodeApplicationStandardsPending,
		},
		{
			name:       "dependency release review required",
			err:        errors.Join(errors.New("create deployment"), state.ErrBindingReleaseRequired),
			wantStatus: http.StatusConflict,
			wantCode:   api.CodeBindingReleaseRequired,
		},
		{
			name:       "terminal app",
			err:        errors.Join(errors.New("create deployment"), state.ErrNotFound),
			wantStatus: http.StatusNotFound,
			wantCode:   api.CodeNotFound,
		},
		{
			name:       "infrastructure failure",
			err:        errors.New("database unavailable"),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   api.CodeCapacity,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			(&server{}).writeDeploymentCreateError(rec, tt.err)
			assertProblem(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestWriteDeploymentCreateErrorLogsTheCause(t *testing.T) {
	var buf bytes.Buffer
	s := &server{log: slog.New(slog.NewJSONHandler(&buf, nil))}
	rec := httptest.NewRecorder()
	s.writeDeploymentCreateError(rec, errors.New("insert deployment: connection reset"))
	assertProblem(t, rec, http.StatusServiceUnavailable, api.CodeCapacity)
	if !strings.Contains(buf.String(), "connection reset") {
		t.Fatalf("log = %q, want the underlying error", buf.String())
	}
}

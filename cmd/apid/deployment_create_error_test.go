package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
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

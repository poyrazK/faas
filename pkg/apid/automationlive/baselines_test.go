// adr: 1302
package automationlive

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type baselineHTTPStore struct {
	calls      int
	actor, key string
	err        error
}

func (s *baselineHTTPStore) GetAutomationLiveBaseline(context.Context, string, string, string) (api.AutomationLiveBaseline, error) {
	s.calls++
	return api.AutomationLiveBaseline{}, s.err
}
func (s *baselineHTTPStore) GetAutomationLiveBaselineHistory(context.Context, string, string, string) (api.AutomationLiveBaselineHistory, error) {
	s.calls++
	return api.AutomationLiveBaselineHistory{}, s.err
}
func (s *baselineHTTPStore) PromoteAutomationLiveBaseline(_ context.Context, app, name, alias, id, actor, key string, request api.PromoteAutomationLiveBaselineRequest) (api.AutomationLiveBaseline, error) {
	s.calls++
	s.actor, s.key = actor, key
	return api.AutomationLiveBaseline{PromotionID: id, Name: name, BaselineName: alias}, s.err
}
func TestBaselineHTTPRejectsForgedMetadataBeforeStore(t *testing.T) {
	for _, body := range []string{`{}`, `{"expected_revision":null}`, `{"expected_revision":0,"actor_account_id":"forged"}`, `{"expected_revision":0,"verdict":"passed"}`, `{"expected_revision":0} {}`, strings.Repeat(" ", int(api.AutomationLiveBaselineRequestMaxBytes)+1)} {
		t.Run(body[:min(len(body), 60)], func(t *testing.T) {
			fake := &baselineHTTPStore{}
			r := httptest.NewRequest("PUT", "/unused", strings.NewReader(body))
			w := httptest.NewRecorder()
			BaselineService{Store: fake}.Promote(w, r, "app", "authenticated-actor", "authenticated-key")
			if w.Code < 400 || fake.calls != 0 {
				t.Fatal("forged or missing concurrency metadata accepted", w.Code, fake.calls)
			}
		})
	}
}
func TestBaselineHTTPScopesActorAndSanitizesErrors(t *testing.T) {
	for _, mode := range []string{"success", "conflict", "missing", "backend"} {
		t.Run(mode, func(t *testing.T) {
			fake := &baselineHTTPStore{}
			want := 200
			switch mode {
			case "conflict":
				fake.err = state.ErrAutomationLiveBaselineConflict
				want = 409
			case "missing":
				fake.err = state.ErrNotFound
				want = 404
			case "backend":
				fake.err = errors.New("PRIVATE storage details")
				want = 503
			}
			r := httptest.NewRequest("PUT", "/unused", strings.NewReader(`{"expected_revision":0}`))
			r.SetPathValue("name", "report")
			r.SetPathValue("baseline", "production")
			r.SetPathValue("promotion_id", "id")
			w := httptest.NewRecorder()
			BaselineService{Store: fake}.Promote(w, r, "app", "authenticated-actor", "authenticated-key")
			if w.Code != want || fake.calls != 1 || fake.actor != "authenticated-actor" || fake.key != "authenticated-key" || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "PRIVATE") {
				t.Fatal("unsafe promotion HTTP", w.Code, w.Body.String())
			}
			if mode == "success" {
				var view api.AutomationLiveBaseline
				if json.Unmarshal(w.Body.Bytes(), &view) != nil || view.PromotionID != "id" {
					t.Fatal("identity lost")
				}
			}
		})
	}
}

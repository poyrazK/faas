package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/gitops"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewEnvironmentGitPlan(w http.ResponseWriter, r *http.Request, account state.Account) {
	store, _ := s.store.(state.EnvironmentGitOpsPreviewStore)
	service := gitops.PreviewService{Store: store, Source: s.environmentGitSourceForRequest, Revision: s.readEnvironmentGitRevision, Decode: decodeJSON}
	service.Preview(w, r, account)
}

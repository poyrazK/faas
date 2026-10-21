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

func (s *server) previewEnvironmentGitPromotion(w http.ResponseWriter, r *http.Request, account state.Account) {
	store, _ := s.store.(state.EnvironmentGitOpsPromotionStore)
	service := gitops.PromotionService{Store: store, Source: s.environmentGitSourceForRequest, Revision: s.readEnvironmentGitRevision, Decode: decodeJSON}
	service.Preview(w, r, account)
}

func (s *server) previewEnvironmentGitRollback(w http.ResponseWriter, r *http.Request, account state.Account) {
	store, _ := s.store.(state.EnvironmentGitOpsRollbackStore)
	service := gitops.RollbackService{Store: store, Source: s.environmentGitSourceForRequest, Revision: s.readEnvironmentGitRevision, Decode: decodeJSON}
	service.Preview(w, r, account)
}

func (s *server) recoveryService() gitops.RecoveryService {
	store, _ := s.store.(state.EnvironmentGitOpsRecoveryStore)
	return gitops.RecoveryService{Store: store, Source: s.environmentGitSourceForRequest, Revision: s.readEnvironmentGitRevision}
}

func (s *server) listEnvironmentGitReleases(w http.ResponseWriter, r *http.Request, account state.Account) {
	s.recoveryService().List(w, r, account)
}

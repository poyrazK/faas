package apidserver

func (s *server) WithPromotionWorkersEnabled(enabled bool) *server {
	s.promotionWorkersEnabled = enabled
	return s
}
func (s *server) promotionWorkerOptions(plan projectEnvironmentPromotionPlan, verify bool, to string) string {
	if !plan.Workers {
		return ""
	}
	if !plan.SyncConfig || !verify || to != "production" {
		return "worker promotion requires --sync-config --verify to production"
	}
	if !s.promotionWorkersEnabled {
		return "worker promotion is disabled pending native qualification"
	}
	return ""
}

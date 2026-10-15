package state

import "context"

func (s *MemStore) MutateOperatorAlertTriage(ctx context.Context, input OperatorAlertTriageMutation) (OperatorAlertTriage, error) {
	if err := ctx.Err(); err != nil {
		return OperatorAlertTriage{}, err
	}
	m, err := normalizeOperatorAlertTriageMutation(input)
	if err != nil {
		return OperatorAlertTriage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Writes never resolve a series alias to its latest recurrence.
	e, ok := s.operatorAlertEpisodes[m.EpisodeID]
	if !ok {
		return OperatorAlertTriage{}, ErrNotFound
	}
	next, err := applyOperatorAlertTriage(e.Triage, m)
	if err != nil {
		return OperatorAlertTriage{}, err
	}
	audit, err := operatorAlertTriageAudit(m, next)
	if err != nil {
		return OperatorAlertTriage{}, err
	}
	if err := ctx.Err(); err != nil {
		return OperatorAlertTriage{}, err
	}
	s.appendAuditLogLocked(audit)
	e.Triage = next
	s.operatorAlertEpisodes[e.ID] = e
	return copyOperatorAlertTriage(next), nil
}

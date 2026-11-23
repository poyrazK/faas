package agentd

// adr: 20261122132055 §8 — the sweep meters the bytes paused sessions keep
// stored, once per UTC hour and account (the first sweep of an hour wins).
// Metered dark: nothing bills it until its price is set.

import "context"

func (s *Sandboxes) meterStorage(ctx context.Context) error {
	n, err := s.store.RecordSandboxStorageHour(ctx)
	if n > 0 {
		s.log.Info("agentd: metered sandbox storage", "accounts", n)
	}
	return err
}

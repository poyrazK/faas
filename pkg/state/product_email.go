package state

import "github.com/onebox-faas/faas/pkg/productemail"

type ProductEmailStore interface{ ProductEmailRepository() productemail.Store }

func (m *MemStore) ProductEmailRepository() productemail.Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.productLocked()
	if d.email == nil {
		d.email = productemail.NewMemory()
	}
	return d.email
}
func (s *PgStore) ProductEmailRepository() productemail.Store {
	return productemail.NewPostgres(s.pool.Pool)
}

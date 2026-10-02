package state

// adr: 431

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/imagechain"
)

func (m *MemStore) PublishBaseImageProducer(ctx context.Context, input BaseImageProducerInput) (BaseImageProducer, error) {
	in, hash, err := prepareBaseImageProducer(input)
	if err != nil {
		return BaseImageProducer{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return BaseImageProducer{}, err
	}
	if old, exists := m.baseImageProducers[in.ID]; exists {
		if old.InputHash != hash || m.baseImageProducerCurrent[in.Artifact.StorageKey] != in.ID {
			return BaseImageProducer{}, ErrConflict
		}
		return cloneBaseImageProducer(old), nil
	}
	if in.ParentProducerID != "" {
		parent, ok := m.baseImageProducers[in.ParentProducerID]
		if !ok {
			return BaseImageProducer{}, ErrNotFound
		}
		if m.baseImageProducerCurrent[parent.Input.Artifact.StorageKey] != parent.ID {
			return BaseImageProducer{}, ErrApplicationStandardRuntimeStale
		}
		if err := checkBaseImageProducerParent(in, parent); err != nil {
			return BaseImageProducer{}, err
		}
	}
	if m.baseImageProducers == nil {
		m.baseImageProducers = map[string]BaseImageProducer{}
	}
	if m.baseImageProducerCurrent == nil {
		m.baseImageProducerCurrent = map[string]string{}
	}
	value := BaseImageProducer{ID: in.ID, InputHash: hash, Input: in, PublishedAt: time.Now().UTC()}
	m.baseImageProducers[value.ID] = value
	m.baseImageProducerCurrent[in.Artifact.StorageKey] = value.ID
	return cloneBaseImageProducer(value), nil
}
func (m *MemStore) GetCurrentBaseImageProducer(ctx context.Context, key string) (BaseImageProducer, error) {
	if !imagechain.ValidBaseKey(key) {
		return BaseImageProducer{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return BaseImageProducer{}, err
	}
	value, ok := m.baseImageProducers[m.baseImageProducerCurrent[key]]
	if !ok {
		return BaseImageProducer{}, ErrNotFound
	}
	if err := validateBaseImageProducer(value); err != nil {
		return BaseImageProducer{}, err
	}
	return cloneBaseImageProducer(value), nil
}
func (m *MemStore) GetBaseImageProducerByID(ctx context.Context, id string) (BaseImageProducer, error) {
	if !validStandardResourceRead(id, id) {
		return BaseImageProducer{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return BaseImageProducer{}, err
	}
	value, ok := m.baseImageProducers[canonicalStandardUUID(id)]
	if !ok {
		return BaseImageProducer{}, ErrNotFound
	}
	if err := validateBaseImageProducer(value); err != nil {
		return BaseImageProducer{}, err
	}
	return cloneBaseImageProducer(value), nil
}

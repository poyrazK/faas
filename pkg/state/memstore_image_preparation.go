package state

import (
	"context"
	"github.com/google/uuid"
	"sort"
	"strings"
	"time"
)

func (m *MemStore) BeginImagePreparation(ctx context.Context, id, node string) (ImagePreparation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ImagePreparation{}, err
	}
	d, ok := m.deployments[id]
	if !ok {
		return ImagePreparation{}, ErrNotFound
	}
	node = strings.TrimSpace(node)
	p, exists := m.imagePreparations[id]
	var prior *ImagePreparation
	if exists {
		prior = &p
	}
	if err := imagePreparationCanBegin(d.Status, prior, node); err != nil {
		return ImagePreparation{}, err
	}
	if !exists {
		inputPath := d.RootfsPath
		if inputPath == "" && d.Kind == DeploymentKindImage {
			inputPath = d.ImageDigest
			if inputPath == "" {
				inputPath = d.SourcePath
			}
		}
		if inputPath == "" {
			return ImagePreparation{}, ErrInvalidArgument
		}
		p = ImagePreparation{DeploymentID: id, NodeName: node, InputPath: inputPath, InputKey: d.RootfsKey, InputBytes: d.RootfsBytes, Phase: ImagePreparing}
	}
	p.ClaimToken, p.UpdatedAt = uuid.NewString(), time.Now().UTC()
	if m.imagePreparations == nil {
		m.imagePreparations = make(map[string]ImagePreparation)
	}
	m.imagePreparations[id] = p
	return p, nil
}

func (m *MemStore) PublishImagePreparationLayer(ctx context.Context, id, token, path, key string, bytes int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if path == "" || key == "" || bytes < 0 {
		return ErrInvalidArgument
	}
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	p, ok := m.imagePreparations[id]
	if !ok || d.Status != DeployImaging || p.ClaimToken != token || p.Phase != ImagePreparing {
		return ErrConflict
	}
	d.RootfsPath, d.RootfsKey, d.RootfsBytes = path, key, bytes
	p.Phase, p.UpdatedAt = ImageLayerPublished, time.Now().UTC()
	m.putDeploymentLocked(id, d)
	m.imagePreparations[id] = p
	return nil
}

func (m *MemStore) AdvanceImagePreparation(ctx context.Context, id, token string, from, to ImagePreparationPhase) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	if !imagePreparationCanAdvance(d.Status, from, to) {
		return ErrInvalidStateTransition
	}
	p, ok := m.imagePreparations[id]
	if !ok || p.ClaimToken != token || p.Phase != from {
		return ErrConflict
	}
	p.Phase, p.UpdatedAt = to, time.Now().UTC()
	m.imagePreparations[id] = p
	return nil
}

func (m *MemStore) ListResumableImagePreparations(ctx context.Context, node string, limit int) ([]BuildImageWork, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, ErrInvalidArgument
	}
	node = strings.TrimSpace(node)
	var rows []ImagePreparation
	for id, p := range m.imagePreparations {
		d, ok := m.deployments[id]
		if !ok || d.Status.IsTerminal() || d.Status == DeployLive || p.Phase == ImageHandedOff {
			continue
		}
		if node != "" && p.NodeName != "" && p.NodeName != node {
			continue
		}
		rows = append(rows, p)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UpdatedAt.Equal(rows[j].UpdatedAt) {
			return rows[i].DeploymentID < rows[j].DeploymentID
		}
		return rows[i].UpdatedAt.Before(rows[j].UpdatedAt)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]BuildImageWork, 0, len(rows))
	for _, p := range rows {
		out = append(out, BuildImageWork{AppID: m.deployments[p.DeploymentID].AppID, DeploymentID: p.DeploymentID, NodeID: p.NodeName})
	}
	return out, nil
}

func (m *MemStore) TransitionImagePreparation(ctx context.Context, id, token string, status DeploymentStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	p, ok := m.imagePreparations[id]
	if !ok || p.ClaimToken != token || !imagePreparationCanTransition(d.Status, p.Phase, status) {
		return ErrConflict
	}
	d.Status, d.Error = status, ""
	m.putDeploymentLocked(id, d)
	return nil
}

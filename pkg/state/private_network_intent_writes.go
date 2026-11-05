package state

import "context"

func (m *MemStore) UpsertAppPrivateNetworkAttachment(ctx context.Context, a AppPrivateNetworkAttachment) (AppPrivateNetworkAttachment, error) {
	return withPrivateNetworkScopeResult(ctx, m, PrivateNetworkAttachmentScope(a.AppID), func(ctx context.Context) (AppPrivateNetworkAttachment, error) {
		return m.upsertAppPrivateNetworkAttachment(ctx, a)
	})
}

func (s *PgStore) UpsertAppPrivateNetworkAttachment(ctx context.Context, a AppPrivateNetworkAttachment) (AppPrivateNetworkAttachment, error) {
	return withPrivateNetworkScopeResult(ctx, s, PrivateNetworkAttachmentScope(a.AppID), func(ctx context.Context) (AppPrivateNetworkAttachment, error) {
		return s.upsertAppPrivateNetworkAttachment(ctx, a)
	})
}

func (m *MemStore) DeleteAppPrivateNetworkAttachment(ctx context.Context, accountID, appID string) error {
	return m.WithPrivateNetworkScope(ctx, PrivateNetworkAttachmentScope(appID), func(ctx context.Context) error {
		return m.deleteAppPrivateNetworkAttachment(ctx, accountID, appID)
	})
}

func (s *PgStore) DeleteAppPrivateNetworkAttachment(ctx context.Context, accountID, appID string) error {
	return s.WithPrivateNetworkScope(ctx, PrivateNetworkAttachmentScope(appID), func(ctx context.Context) error {
		return s.deleteAppPrivateNetworkAttachment(ctx, accountID, appID)
	})
}

func (m *MemStore) CreatePrivateNetworkPeering(ctx context.Context, p PrivateNetworkPeering) (PrivateNetworkPeering, error) {
	return withPrivateNetworkScopeResult(ctx, m, PrivateNetworkPeeringScope(p.AccountID, p.Region), func(ctx context.Context) (PrivateNetworkPeering, error) {
		return m.createPrivateNetworkPeering(ctx, p)
	})
}

func (s *PgStore) CreatePrivateNetworkPeering(ctx context.Context, p PrivateNetworkPeering) (PrivateNetworkPeering, error) {
	return withPrivateNetworkScopeResult(ctx, s, PrivateNetworkPeeringScope(p.AccountID, p.Region), func(ctx context.Context) (PrivateNetworkPeering, error) {
		return s.createPrivateNetworkPeering(ctx, p)
	})
}

type privateNetworkPeeringDeletionCoordinator interface {
	PrivateNetworkCoordinator
	GetPrivateNetworkPeering(context.Context, string, string) (PrivateNetworkPeering, error)
}

func withPrivateNetworkPeeringDeletion(ctx context.Context, store privateNetworkPeeringDeletionCoordinator, accountID, id string, fn func(context.Context) error) error {
	peer, err := store.GetPrivateNetworkPeering(ctx, accountID, id)
	if err != nil {
		return err
	}
	return store.WithPrivateNetworkScope(ctx, PrivateNetworkPeeringScope(accountID, peer.Region), func(ctx context.Context) error {
		fresh, err := store.GetPrivateNetworkPeering(ctx, accountID, id)
		if err != nil {
			return err
		}
		if fresh.Region != peer.Region {
			return ErrConflict
		}
		return fn(ctx)
	})
}

func (m *MemStore) DeletePrivateNetworkPeering(ctx context.Context, accountID, id string) error {
	return withPrivateNetworkPeeringDeletion(ctx, m, accountID, id, func(ctx context.Context) error {
		return m.deletePrivateNetworkPeering(ctx, accountID, id)
	})
}

func (s *PgStore) DeletePrivateNetworkPeering(ctx context.Context, accountID, id string) error {
	return withPrivateNetworkPeeringDeletion(ctx, s, accountID, id, func(ctx context.Context) error {
		return s.deletePrivateNetworkPeering(ctx, accountID, id)
	})
}

func (s *PgStore) DeletePrivateNetworkPeeringDurably(ctx context.Context, accountID, id string) error {
	return withPrivateNetworkPeeringDeletion(ctx, s, accountID, id, func(ctx context.Context) error {
		return s.deletePrivateNetworkPeeringDurably(ctx, accountID, id)
	})
}

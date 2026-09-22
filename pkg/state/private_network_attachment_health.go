package state

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

const (
	privateNetworkNodeStatusReady = "ready"
	privateNetworkNodeStatusError = "error"
)

func validatePrivateNetworkAttachmentNodeStatus(in PrivateNetworkAttachmentNodeStatus) (PrivateNetworkAttachmentNodeStatus, error) {
	in.AccountID = strings.TrimSpace(in.AccountID)
	in.AppID = strings.TrimSpace(in.AppID)
	in.NetworkID = strings.TrimSpace(in.NetworkID)
	in.NodeID = strings.TrimSpace(in.NodeID)
	in.FabricStatus = strings.TrimSpace(in.FabricStatus)
	in.FabricDetail = strings.TrimSpace(in.FabricDetail)
	in.RouteStatus = strings.TrimSpace(in.RouteStatus)
	in.RouteDetail = strings.TrimSpace(in.RouteDetail)
	if in.AccountID == "" || in.AppID == "" || in.NetworkID == "" || in.NodeID == "" {
		return PrivateNetworkAttachmentNodeStatus{}, ErrInvalidArgument
	}
	for _, status := range []string{in.FabricStatus, in.RouteStatus} {
		if status != "" && status != privateNetworkNodeStatusReady && status != privateNetworkNodeStatusError {
			return PrivateNetworkAttachmentNodeStatus{}, ErrInvalidArgument
		}
	}
	if in.ObservedAt.IsZero() {
		in.ObservedAt = time.Now().UTC()
	} else {
		in.ObservedAt = in.ObservedAt.UTC()
	}
	return in, nil
}

func privateNetworkAttachmentNodeStatusKey(accountID, appID, nodeID string) string {
	return accountID + "\x00" + appID + "\x00" + nodeID
}

func clonePrivateNetworkAttachmentNodeStatus(in PrivateNetworkAttachmentNodeStatus) PrivateNetworkAttachmentNodeStatus {
	return in
}

func (m *MemStore) UpsertPrivateNetworkAttachmentNodeStatus(ctx context.Context, observation PrivateNetworkAttachmentNodeStatus) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var err error
	observation, err = validatePrivateNetworkAttachmentNodeStatus(observation)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.privateNetworkAttachmentNodeStatuses == nil {
		m.privateNetworkAttachmentNodeStatuses = make(map[string]PrivateNetworkAttachmentNodeStatus)
	}
	key := privateNetworkAttachmentNodeStatusKey(observation.AccountID, observation.AppID, observation.NodeID)
	if existing, ok := m.privateNetworkAttachmentNodeStatuses[key]; ok {
		if existing.AccountID != observation.AccountID || existing.AppID != observation.AppID {
			return errors.New("state: private network node status ownership conflict")
		}
		if existing.NetworkID == observation.NetworkID && observation.FabricStatus == "" {
			observation.FabricStatus = existing.FabricStatus
			observation.FabricDetail = existing.FabricDetail
		}
		if existing.NetworkID == observation.NetworkID && observation.RouteStatus == "" {
			observation.RouteStatus = existing.RouteStatus
			observation.RouteDetail = existing.RouteDetail
		}
	}
	m.privateNetworkAttachmentNodeStatuses[key] = clonePrivateNetworkAttachmentNodeStatus(observation)
	return nil
}

func (m *MemStore) ListPrivateNetworkAttachmentNodeStatuses(ctx context.Context, accountID, appID string) ([]PrivateNetworkAttachmentNodeStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	accountID = strings.TrimSpace(accountID)
	appID = strings.TrimSpace(appID)
	if accountID == "" || appID == "" {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PrivateNetworkAttachmentNodeStatus, 0)
	for _, observation := range m.privateNetworkAttachmentNodeStatuses {
		if observation.AccountID == accountID && observation.AppID == appID {
			out = append(out, clonePrivateNetworkAttachmentNodeStatus(observation))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, nil
}

func (m *MemStore) DeletePrivateNetworkAttachmentNodeStatuses(ctx context.Context, accountID, appID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	accountID = strings.TrimSpace(accountID)
	appID = strings.TrimSpace(appID)
	if accountID == "" || appID == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, observation := range m.privateNetworkAttachmentNodeStatuses {
		if observation.AccountID == accountID && observation.AppID == appID {
			delete(m.privateNetworkAttachmentNodeStatuses, key)
		}
	}
	return nil
}

func (s *PgStore) UpsertPrivateNetworkAttachmentNodeStatus(ctx context.Context, observation PrivateNetworkAttachmentNodeStatus) error {
	var err error
	observation, err = validatePrivateNetworkAttachmentNodeStatus(observation)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		insert into app_private_network_attachment_nodes
		       (account_id, app_id, network_id, node_id, fabric_status, fabric_detail,
		        route_status, route_detail, observed_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		on conflict (app_id, node_id) do update set
		       network_id = excluded.network_id,
		       fabric_status = case when excluded.network_id <> app_private_network_attachment_nodes.network_id or excluded.fabric_status <> '' then excluded.fabric_status else app_private_network_attachment_nodes.fabric_status end,
		       fabric_detail = case when excluded.network_id <> app_private_network_attachment_nodes.network_id or excluded.fabric_status <> '' then excluded.fabric_detail else app_private_network_attachment_nodes.fabric_detail end,
		       route_status = case when excluded.network_id <> app_private_network_attachment_nodes.network_id or excluded.route_status <> '' then excluded.route_status else app_private_network_attachment_nodes.route_status end,
		       route_detail = case when excluded.network_id <> app_private_network_attachment_nodes.network_id or excluded.route_status <> '' then excluded.route_detail else app_private_network_attachment_nodes.route_detail end,
		       observed_at = excluded.observed_at
		 where app_private_network_attachment_nodes.account_id = excluded.account_id`,
		observation.AccountID, observation.AppID, observation.NetworkID, observation.NodeID,
		observation.FabricStatus, observation.FabricDetail, observation.RouteStatus,
		observation.RouteDetail, observation.ObservedAt)
	return mapErr(err)
}

func (s *PgStore) ListPrivateNetworkAttachmentNodeStatuses(ctx context.Context, accountID, appID string) ([]PrivateNetworkAttachmentNodeStatus, error) {
	accountID = strings.TrimSpace(accountID)
	appID = strings.TrimSpace(appID)
	if accountID == "" || appID == "" {
		return nil, ErrInvalidArgument
	}
	rows, err := s.pool.Query(ctx, `
		select account_id, app_id, network_id, node_id, fabric_status, fabric_detail,
		       route_status, route_detail, observed_at
		  from app_private_network_attachment_nodes
		 where account_id = $1 and app_id = $2
		 order by node_id`, accountID, appID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]PrivateNetworkAttachmentNodeStatus, 0)
	for rows.Next() {
		var observation PrivateNetworkAttachmentNodeStatus
		if err := rows.Scan(&observation.AccountID, &observation.AppID, &observation.NetworkID, &observation.NodeID, &observation.FabricStatus, &observation.FabricDetail, &observation.RouteStatus, &observation.RouteDetail, &observation.ObservedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

func (s *PgStore) DeletePrivateNetworkAttachmentNodeStatuses(ctx context.Context, accountID, appID string) error {
	accountID = strings.TrimSpace(accountID)
	appID = strings.TrimSpace(appID)
	if accountID == "" || appID == "" {
		return ErrInvalidArgument
	}
	_, err := s.pool.Exec(ctx, `delete from app_private_network_attachment_nodes where account_id = $1 and app_id = $2`, accountID, appID)
	return mapErr(err)
}

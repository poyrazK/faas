package main

import "github.com/onebox-faas/faas/pkg/api"

var secretDeliveryFilterValues = []string{"delivered", "pending", "failed", "unknown"}

func validSecretDeliveryFilter(value string) bool {
	if value == "" {
		return true
	}
	for _, allowed := range secretDeliveryFilterValues {
		if value == allowed {
			return true
		}
	}
	return false
}

func secretDeliveryState(status string, current, observed int64) string {
	switch status {
	case "delivered":
		if current > 0 && current == observed {
			return "delivered"
		}
	case "pending", "failed":
		return status
	}
	return "unknown"
}

func filterAppSecretListByDelivery(resp *api.AppSecretListResponse, status string) {
	if status == "" {
		return
	}
	resp.Count = 0
	if len(resp.SecretsByScope) > 0 {
		for scope, rows := range resp.SecretsByScope {
			kept := rows[:0]
			for _, row := range rows {
				if secretDeliveryState(row.DeliveryStatus, row.DeliveryVersion, row.DeliveredVersion) == status {
					kept = append(kept, row)
				}
			}
			resp.SecretsByScope[scope] = kept
			resp.Count += len(kept)
		}
		return
	}
	kept := resp.Secrets[:0]
	for _, row := range resp.Secrets {
		if secretDeliveryState(row.DeliveryStatus, row.DeliveryVersion, row.DeliveredVersion) == status {
			kept = append(kept, row)
		}
	}
	resp.Secrets = kept
	resp.Count = len(kept)
}

package main

import "github.com/onebox-faas/faas/pkg/api"

func filterAppSecretListByKeys(resp *api.AppSecretListResponse, keys []string) {
	if len(keys) == 0 {
		return
	}
	selected := make(map[string]bool, len(keys))
	for _, key := range keys {
		selected[key] = true
	}
	resp.Count = 0
	if len(resp.SecretsByScope) > 0 {
		for scope, rows := range resp.SecretsByScope {
			kept := rows[:0]
			for _, row := range rows {
				if selected[row.Key] {
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
		if selected[row.Key] {
			kept = append(kept, row)
		}
	}
	resp.Secrets = kept
	resp.Count = len(kept)
}

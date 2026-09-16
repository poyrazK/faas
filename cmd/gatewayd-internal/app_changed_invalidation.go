package main

import (
	"github.com/onebox-faas/faas/pkg/db"
)

// appChangedID accepts APID's documented envelope and the legacy bare ID
// emitted by the maintenance-mode trigger. Empty signals a full cache flush.
func appChangedID(payload string) (string, error) {
	event, err := db.ParseAppChangedPayload(payload)
	if err != nil {
		return "", err
	}
	return event.AppID, nil
}

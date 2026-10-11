package main

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

func privateRuntimeIngressIdentity(getenv func(string) string, slotID, sessionID string) (http.Handler, error) {
	if getenv("FAAS_RUNTIME_UPGRADE_INGRESS_CONFIRMATION") != "1" {
		return nil, nil
	}
	if getenv("FAAS_RUNTIME_UPGRADE_DRAIN_CONFIRMATION") != "1" || getenv("FAAS_RUNTIME_UPGRADE_ROUTING_CONFIRMATION") != "1" {
		return nil, fmt.Errorf("private ingress identity requires routing and drain confirmation")
	}
	return ingress.NewIdentityHandler(getenv("FAAS_RUNTIME_UPGRADE_INGRESS_TOKEN"), slotID, sessionID)
}

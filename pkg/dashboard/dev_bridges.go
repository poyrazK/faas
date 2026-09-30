package dashboard

import "github.com/onebox-faas/faas/pkg/devbridge"

type DevBridgesData struct {
	Enabled   bool
	Sessions  []DevBridgeItem
	Selected  *DevBridgeItem
	CSRFToken string
	Flash     string
}

type DevBridgeItem struct {
	Session                   devbridge.Session
	Activity                  devbridge.Activity
	App, Project, Environment string
}

package dashboard

import (
	"github.com/onebox-faas/faas/pkg/dataapi"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type DataAPIData struct {
	Slug, CSRFToken, Message, TaskID, TaskStatus, Fingerprint string
	Enabled, TypesReady                                       bool
	RefreshSeconds                                            int
	Bindings                                                  []managedpostgres.AppBindingInventory
	Schema                                                    *dataapi.Snapshot
}

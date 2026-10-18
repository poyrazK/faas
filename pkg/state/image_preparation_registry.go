// adr: 435 — retain exact registry evidence with the durable main-layer checkpoint.
package state

import "context"

type RegistryImagePreparationStore interface {
	PublishRegistryImagePreparationLayer(context.Context, DeploymentRegistryRootfsInput, string) (DeploymentRegistryRootfs, error)
}

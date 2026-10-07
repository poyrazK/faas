// adr: 435 — source evidence and the durable conversion checkpoint publish together.
package state

import "context"

type SourceImagePreparationStore interface {
	PublishSourceImagePreparationLayer(context.Context, SourceBuildRootfsInput, string) (SourceBuildRootfs, error)
}

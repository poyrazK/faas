package state

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrProductCatalogStale = errors.New("state: reviewed Product Control catalog head changed")

type ProductSetupStore interface {
	PublishProductCatalog(context.Context, string, string, api.ProductCatalogSpec, string) (api.ProductCatalog, error)
}

func validateProductExpectedHead(head string) error {
	if head == "" {
		return nil
	}
	raw, err := hex.DecodeString(head)
	if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != head {
		return ErrInvalidArgument
	}
	return nil
}

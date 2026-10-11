package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/operations"
)

func configureOperationRoutes(handler *gateway.Handler, deps runDeps) error {
	path := ""
	if deps.config != nil {
		path = deps.config.OperationsPreviewPolicyPath
	}
	gate, err := operations.NewPreviewAdmission(path)
	if err != nil {
		return err
	}
	if deps.pgStore == nil {
		if path != "" {
			return fmt.Errorf("operations preview configuration requires durable storage")
		}
		return nil
	}
	// Install the resolver even when closed: deleting configuration must not
	// turn an existing operation route into an ordinary handler invocation.
	handler.WithOperationRoutes(gateway.DurableOperationRoutes{Store: deps.pgStore, Admission: gate})
	return nil
}

package managedpostgres

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
)

func WriteCapacityProblem(w http.ResponseWriter, err error) bool {
	code, detail := "", ""
	switch {
	case errors.Is(err, connectioncapacity.ErrUnknown):
		code = "managed_postgres_capacity_needs_qualification"
		detail = "Run a connection check to refresh capacity. The operator must qualify this database allocation and all external consumers."
	case errors.Is(err, connectioncapacity.ErrInsufficient):
		code = "managed_postgres_insufficient_capacity"
		detail = "Release an unused binding or qualify a larger database allocation before adding another connection consumer."
	default:
		return false
	}
	api.WriteProblem(w, api.NewProblem(409, code, "Database connection capacity", detail).WithDocs("https://gregale.dev/docs/data-pipelines"))
	return true
}

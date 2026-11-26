//go:build linux

package main

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/dataapiinspect"
)

func executeDataAPIWriteInspection(ctx context.Context, req apptaskproto.Request, manifest api.AppManifest, secrets, apiEnv map[string]string, stdout io.Writer) (apptaskproto.Result, error) {
	fail := func() (apptaskproto.Result, error) {
		code := 1
		return apptaskproto.Result{Status: apptaskproto.StatusFailed, ExitCode: &code, FailureCode: "data_api_inspection_unavailable", FailureMessage: "Data API inspection unavailable"}, nil
	}
	if req.Version != apptaskproto.DataAPIInspectionVersion || !api.IsDataAPIWriteInspectionCommand(req.Command, req.CommandShell) || req.Interactive {
		return fail()
	}
	env := BuildEnvWithSecrets(os.Environ(), manifest, secrets, apiEnv)
	dsn := appTaskEnvValue(env, "DATABASE_URL")
	observation, err := dataapiinspect.Run(ctx, dsn, req.Command[1], req.Command[2], req.DataAPIInspectionCA)
	if err != nil {
		return fail()
	}
	if err := json.NewEncoder(stdout).Encode(observation); err != nil {
		return apptaskproto.Result{}, err
	}
	code := 0
	return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &code}, nil
}

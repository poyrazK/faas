package main

import "github.com/onebox-faas/faas/pkg/api"

// RenderDoctorDeploy renders the doctor report as deploy prints it.
var RenderDoctorDeploy = renderDoctorDeploy

// PrintSecretDeliveryOverview prints the env list delivery line to OsStdout.
func PrintSecretDeliveryOverview(app, scope string, resp *api.AppSecretListResponse) {
	printSecretDeliveryOverview(app, scope, resp)
}

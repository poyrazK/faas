package api

const AppTaskServiceBindingProbeCommand = "__gregale_service_binding_probe_v1__"

type ServiceBindingProbeCheck struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type ServiceBindingProbeReport struct {
	TargetDeploymentID string                   `json:"target_deployment_id,omitempty"`
	App                string                   `json:"app,omitempty"`
	Service            string                   `json:"service"`
	URL                string                   `json:"url"`
	TaskID             string                   `json:"task_id,omitempty"`
	DeploymentID       string                   `json:"deployment_id,omitempty"`
	DNS                ServiceBindingProbeCheck `json:"dns"`
	TLS                ServiceBindingProbeCheck `json:"tls"`
	Authorization      ServiceBindingProbeCheck `json:"authorization"`
	Routing            ServiceBindingProbeCheck `json:"routing"`
	HTTPStatus         int                      `json:"http_status,omitempty"`
	Error              string                   `json:"error,omitempty"`
}

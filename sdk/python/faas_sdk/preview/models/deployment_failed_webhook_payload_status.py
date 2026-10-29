from typing import Literal

DeploymentFailedWebhookPayloadStatus = Literal["failed"]

DEPLOYMENT_FAILED_WEBHOOK_PAYLOAD_STATUS_VALUES: set[DeploymentFailedWebhookPayloadStatus] = {
    "failed",
}


def check_deployment_failed_webhook_payload_status(value: str) -> DeploymentFailedWebhookPayloadStatus:
    if value in DEPLOYMENT_FAILED_WEBHOOK_PAYLOAD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_FAILED_WEBHOOK_PAYLOAD_STATUS_VALUES!r}")

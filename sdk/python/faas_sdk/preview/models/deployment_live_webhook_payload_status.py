from typing import Literal

DeploymentLiveWebhookPayloadStatus = Literal["live"]

DEPLOYMENT_LIVE_WEBHOOK_PAYLOAD_STATUS_VALUES: set[DeploymentLiveWebhookPayloadStatus] = {
    "live",
}


def check_deployment_live_webhook_payload_status(value: str) -> DeploymentLiveWebhookPayloadStatus:
    if value in DEPLOYMENT_LIVE_WEBHOOK_PAYLOAD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_LIVE_WEBHOOK_PAYLOAD_STATUS_VALUES!r}")

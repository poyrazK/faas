from typing import Literal

DeploymentResponseTag = Literal[
    "compliance_hold", "hotfix", "incident_recovery", "partner_request", "scheduled_maintenance"
]

DEPLOYMENT_RESPONSE_TAG_VALUES: set[DeploymentResponseTag] = {
    "compliance_hold",
    "hotfix",
    "incident_recovery",
    "partner_request",
    "scheduled_maintenance",
}


def check_deployment_response_tag(value: str) -> DeploymentResponseTag:
    if value in DEPLOYMENT_RESPONSE_TAG_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_RESPONSE_TAG_VALUES!r}")

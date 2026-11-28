from typing import Literal

CreateDeploymentRequestTagType1 = Literal[
    "compliance_hold", "hotfix", "incident_recovery", "partner_request", "scheduled_maintenance"
]

CREATE_DEPLOYMENT_REQUEST_TAG_TYPE_1_VALUES: set[CreateDeploymentRequestTagType1] = {
    "compliance_hold",
    "hotfix",
    "incident_recovery",
    "partner_request",
    "scheduled_maintenance",
}


def check_create_deployment_request_tag_type_1(value: str) -> CreateDeploymentRequestTagType1:
    if value in CREATE_DEPLOYMENT_REQUEST_TAG_TYPE_1_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_DEPLOYMENT_REQUEST_TAG_TYPE_1_VALUES!r}")

from typing import Literal

SourceRefDeployRequestTag = Literal[
    "compliance_hold", "hotfix", "incident_recovery", "partner_request", "scheduled_maintenance"
]

SOURCE_REF_DEPLOY_REQUEST_TAG_VALUES: set[SourceRefDeployRequestTag] = {
    "compliance_hold",
    "hotfix",
    "incident_recovery",
    "partner_request",
    "scheduled_maintenance",
}


def check_source_ref_deploy_request_tag(value: str) -> SourceRefDeployRequestTag:
    if value in SOURCE_REF_DEPLOY_REQUEST_TAG_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SOURCE_REF_DEPLOY_REQUEST_TAG_VALUES!r}")

from typing import Literal

SourceTarballDeployRequestTag = Literal[
    "compliance_hold", "hotfix", "incident_recovery", "partner_request", "scheduled_maintenance"
]

SOURCE_TARBALL_DEPLOY_REQUEST_TAG_VALUES: set[SourceTarballDeployRequestTag] = {
    "compliance_hold",
    "hotfix",
    "incident_recovery",
    "partner_request",
    "scheduled_maintenance",
}


def check_source_tarball_deploy_request_tag(value: str) -> SourceTarballDeployRequestTag:
    if value in SOURCE_TARBALL_DEPLOY_REQUEST_TAG_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SOURCE_TARBALL_DEPLOY_REQUEST_TAG_VALUES!r}")

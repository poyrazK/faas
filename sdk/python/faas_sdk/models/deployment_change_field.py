from typing import Literal

DeploymentChangeField = Literal[
    "build_id",
    "build_plan",
    "canary_preset",
    "commit_sha",
    "has_overrides",
    "image_digest",
    "kind",
    "min_instances",
    "rollback_on_5xx",
    "rollout_state",
    "scope",
    "source_root",
    "source_url",
    "status",
    "traffic_percent",
]

DEPLOYMENT_CHANGE_FIELD_VALUES: set[DeploymentChangeField] = {
    "build_id",
    "build_plan",
    "canary_preset",
    "commit_sha",
    "has_overrides",
    "image_digest",
    "kind",
    "min_instances",
    "rollback_on_5xx",
    "rollout_state",
    "scope",
    "source_root",
    "source_url",
    "status",
    "traffic_percent",
}


def check_deployment_change_field(value: str) -> DeploymentChangeField:
    if value in DEPLOYMENT_CHANGE_FIELD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_CHANGE_FIELD_VALUES!r}")

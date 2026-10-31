from typing import Literal

SourceRefDeployRequestNativeRollout = Literal["service", "worker"]

SOURCE_REF_DEPLOY_REQUEST_NATIVE_ROLLOUT_VALUES: set[SourceRefDeployRequestNativeRollout] = {
    "service",
    "worker",
}


def check_source_ref_deploy_request_native_rollout(value: str) -> SourceRefDeployRequestNativeRollout:
    if value in SOURCE_REF_DEPLOY_REQUEST_NATIVE_ROLLOUT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SOURCE_REF_DEPLOY_REQUEST_NATIVE_ROLLOUT_VALUES!r}")

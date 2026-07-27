from typing import Literal

CreateDeploymentFilesBodyKind = Literal["app", "function"]

CREATE_DEPLOYMENT_FILES_BODY_KIND_VALUES: set[CreateDeploymentFilesBodyKind] = {
    "app",
    "function",
}


def check_create_deployment_files_body_kind(value: str) -> CreateDeploymentFilesBodyKind:
    if value in CREATE_DEPLOYMENT_FILES_BODY_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_DEPLOYMENT_FILES_BODY_KIND_VALUES!r}")

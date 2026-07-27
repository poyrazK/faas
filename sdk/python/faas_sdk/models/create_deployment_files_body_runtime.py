from typing import Literal

CreateDeploymentFilesBodyRuntime = Literal["go124", "node22", "python312"]

CREATE_DEPLOYMENT_FILES_BODY_RUNTIME_VALUES: set[CreateDeploymentFilesBodyRuntime] = {
    "go124",
    "node22",
    "python312",
}


def check_create_deployment_files_body_runtime(value: str) -> CreateDeploymentFilesBodyRuntime:
    if value in CREATE_DEPLOYMENT_FILES_BODY_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_DEPLOYMENT_FILES_BODY_RUNTIME_VALUES!r}")

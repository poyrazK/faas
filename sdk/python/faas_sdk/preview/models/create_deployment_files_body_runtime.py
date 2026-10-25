from typing import Literal

CreateDeploymentFilesBodyRuntime = Literal[
    "binary", "go124", "go124-alpine", "node22", "node24", "python312", "python313"
]

CREATE_DEPLOYMENT_FILES_BODY_RUNTIME_VALUES: set[CreateDeploymentFilesBodyRuntime] = {
    "binary",
    "go124",
    "go124-alpine",
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_create_deployment_files_body_runtime(value: str) -> CreateDeploymentFilesBodyRuntime:
    if value in CREATE_DEPLOYMENT_FILES_BODY_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_DEPLOYMENT_FILES_BODY_RUNTIME_VALUES!r}")

from typing import Literal

DeployCellAppFilesBodyRuntime = Literal["go124", "go124-alpine", "node22", "node24", "python312", "python313"]

DEPLOY_CELL_APP_FILES_BODY_RUNTIME_VALUES: set[DeployCellAppFilesBodyRuntime] = {
    "go124",
    "go124-alpine",
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_deploy_cell_app_files_body_runtime(value: str) -> DeployCellAppFilesBodyRuntime:
    if value in DEPLOY_CELL_APP_FILES_BODY_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOY_CELL_APP_FILES_BODY_RUNTIME_VALUES!r}")

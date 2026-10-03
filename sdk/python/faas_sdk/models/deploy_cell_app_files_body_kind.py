from typing import Literal

DeployCellAppFilesBodyKind = Literal["app", "function"]

DEPLOY_CELL_APP_FILES_BODY_KIND_VALUES: set[DeployCellAppFilesBodyKind] = {
    "app",
    "function",
}


def check_deploy_cell_app_files_body_kind(value: str) -> DeployCellAppFilesBodyKind:
    if value in DEPLOY_CELL_APP_FILES_BODY_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOY_CELL_APP_FILES_BODY_KIND_VALUES!r}")

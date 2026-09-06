from typing import Literal

DeployDevSourceBodyRuntime = Literal["go124", "go124-alpine", "node22", "node24", "python312", "python313"]

DEPLOY_DEV_SOURCE_BODY_RUNTIME_VALUES: set[DeployDevSourceBodyRuntime] = {
    "go124",
    "go124-alpine",
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_deploy_dev_source_body_runtime(value: str) -> DeployDevSourceBodyRuntime:
    if value in DEPLOY_DEV_SOURCE_BODY_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOY_DEV_SOURCE_BODY_RUNTIME_VALUES!r}")

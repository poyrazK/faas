from typing import Literal

ExecutionTaskVersionResponseArtifactStorage = Literal["inline", "stored"]

EXECUTION_TASK_VERSION_RESPONSE_ARTIFACT_STORAGE_VALUES: set[ExecutionTaskVersionResponseArtifactStorage] = {
    "inline",
    "stored",
}


def check_execution_task_version_response_artifact_storage(value: str) -> ExecutionTaskVersionResponseArtifactStorage:
    if value in EXECUTION_TASK_VERSION_RESPONSE_ARTIFACT_STORAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EXECUTION_TASK_VERSION_RESPONSE_ARTIFACT_STORAGE_VALUES!r}"
    )

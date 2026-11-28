from typing import Literal

TaskReleaseReviewWorkerRuntime = Literal["node"]

TASK_RELEASE_REVIEW_WORKER_RUNTIME_VALUES: set[TaskReleaseReviewWorkerRuntime] = {
    "node",
}


def check_task_release_review_worker_runtime(value: str) -> TaskReleaseReviewWorkerRuntime:
    if value in TASK_RELEASE_REVIEW_WORKER_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_RELEASE_REVIEW_WORKER_RUNTIME_VALUES!r}")

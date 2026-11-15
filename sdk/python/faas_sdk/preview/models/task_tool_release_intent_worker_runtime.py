from typing import Literal

TaskToolReleaseIntentWorkerRuntime = Literal["node"]

TASK_TOOL_RELEASE_INTENT_WORKER_RUNTIME_VALUES: set[TaskToolReleaseIntentWorkerRuntime] = {
    "node",
}


def check_task_tool_release_intent_worker_runtime(value: str) -> TaskToolReleaseIntentWorkerRuntime:
    if value in TASK_TOOL_RELEASE_INTENT_WORKER_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_INTENT_WORKER_RUNTIME_VALUES!r}")

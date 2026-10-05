from typing import Literal

ProjectEnvironmentQueueBindingWorkloadClass = Literal["http", "job", "worker"]

PROJECT_ENVIRONMENT_QUEUE_BINDING_WORKLOAD_CLASS_VALUES: set[ProjectEnvironmentQueueBindingWorkloadClass] = {
    "http",
    "job",
    "worker",
}


def check_project_environment_queue_binding_workload_class(value: str) -> ProjectEnvironmentQueueBindingWorkloadClass:
    if value in PROJECT_ENVIRONMENT_QUEUE_BINDING_WORKLOAD_CLASS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_QUEUE_BINDING_WORKLOAD_CLASS_VALUES!r}"
    )

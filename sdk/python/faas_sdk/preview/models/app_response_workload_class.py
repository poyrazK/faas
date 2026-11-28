from typing import Literal

AppResponseWorkloadClass = Literal["graphql", "grpc", "http", "job", "worker"]

APP_RESPONSE_WORKLOAD_CLASS_VALUES: set[AppResponseWorkloadClass] = {
    "graphql",
    "grpc",
    "http",
    "job",
    "worker",
}


def check_app_response_workload_class(value: str) -> AppResponseWorkloadClass:
    if value in APP_RESPONSE_WORKLOAD_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_WORKLOAD_CLASS_VALUES!r}")

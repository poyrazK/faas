from typing import Literal

PlanWorkloadClass = Literal["graphql", "grpc", "http", "job", "server", "unknown", "worker"]

PLAN_WORKLOAD_CLASS_VALUES: set[PlanWorkloadClass] = {
    "graphql",
    "grpc",
    "http",
    "job",
    "server",
    "unknown",
    "worker",
}


def check_plan_workload_class(value: str) -> PlanWorkloadClass:
    if value in PLAN_WORKLOAD_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLAN_WORKLOAD_CLASS_VALUES!r}")

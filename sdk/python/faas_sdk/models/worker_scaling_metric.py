from typing import Literal

WorkerScalingMetric = Literal["queue_depth", "queue_lag"]

WORKER_SCALING_METRIC_VALUES: set[WorkerScalingMetric] = {
    "queue_depth",
    "queue_lag",
}


def check_worker_scaling_metric(value: str) -> WorkerScalingMetric:
    if value in WORKER_SCALING_METRIC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKER_SCALING_METRIC_VALUES!r}")

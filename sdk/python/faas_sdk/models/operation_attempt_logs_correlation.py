from typing import Literal

OperationAttemptLogsCorrelation = Literal["instance_window"]

OPERATION_ATTEMPT_LOGS_CORRELATION_VALUES: set[OperationAttemptLogsCorrelation] = {
    "instance_window",
}


def check_operation_attempt_logs_correlation(value: str) -> OperationAttemptLogsCorrelation:
    if value in OPERATION_ATTEMPT_LOGS_CORRELATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_ATTEMPT_LOGS_CORRELATION_VALUES!r}")

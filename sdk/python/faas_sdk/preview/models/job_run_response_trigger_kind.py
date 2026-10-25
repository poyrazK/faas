from typing import Literal

JobRunResponseTriggerKind = Literal["manual", "scheduled", "triggered"]

JOB_RUN_RESPONSE_TRIGGER_KIND_VALUES: set[JobRunResponseTriggerKind] = {
    "manual",
    "scheduled",
    "triggered",
}


def check_job_run_response_trigger_kind(value: str) -> JobRunResponseTriggerKind:
    if value in JOB_RUN_RESPONSE_TRIGGER_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {JOB_RUN_RESPONSE_TRIGGER_KIND_VALUES!r}")

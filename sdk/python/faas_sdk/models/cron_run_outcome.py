from typing import Literal

CronRunOutcome = Literal["cancelled", "dead_letter", "failed", "running", "success", "timeout"]

CRON_RUN_OUTCOME_VALUES: set[CronRunOutcome] = {
    "cancelled",
    "dead_letter",
    "failed",
    "running",
    "success",
    "timeout",
}


def check_cron_run_outcome(value: str) -> CronRunOutcome:
    if value in CRON_RUN_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CRON_RUN_OUTCOME_VALUES!r}")

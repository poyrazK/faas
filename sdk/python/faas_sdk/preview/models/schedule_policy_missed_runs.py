from typing import Literal

SchedulePolicyMissedRuns = Literal["coalesce_latest", "skip"]

SCHEDULE_POLICY_MISSED_RUNS_VALUES: set[SchedulePolicyMissedRuns] = {
    "coalesce_latest",
    "skip",
}


def check_schedule_policy_missed_runs(value: str) -> SchedulePolicyMissedRuns:
    if value in SCHEDULE_POLICY_MISSED_RUNS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCHEDULE_POLICY_MISSED_RUNS_VALUES!r}")

from typing import Literal

DatabaseSwitchPhase = Literal[
    "aborting",
    "cancelled",
    "completed",
    "draining",
    "failed",
    "fencing",
    "restoring",
    "resuming",
    "settling",
    "staging",
    "switching",
    "verifying",
]

DATABASE_SWITCH_PHASE_VALUES: set[DatabaseSwitchPhase] = {
    "aborting",
    "cancelled",
    "completed",
    "draining",
    "failed",
    "fencing",
    "restoring",
    "resuming",
    "settling",
    "staging",
    "switching",
    "verifying",
}


def check_database_switch_phase(value: str) -> DatabaseSwitchPhase:
    if value in DATABASE_SWITCH_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATABASE_SWITCH_PHASE_VALUES!r}")

from typing import Literal

MigrationCutoverActionRequestAction = Literal["cancel", "complete", "confirm_restored", "start_checks"]

MIGRATION_CUTOVER_ACTION_REQUEST_ACTION_VALUES: set[MigrationCutoverActionRequestAction] = {
    "cancel",
    "complete",
    "confirm_restored",
    "start_checks",
}


def check_migration_cutover_action_request_action(value: str) -> MigrationCutoverActionRequestAction:
    if value in MIGRATION_CUTOVER_ACTION_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MIGRATION_CUTOVER_ACTION_REQUEST_ACTION_VALUES!r}")

from typing import Literal

WorkflowScheduleResponseCatchUp = Literal["latest", "skip"]

WORKFLOW_SCHEDULE_RESPONSE_CATCH_UP_VALUES: set[WorkflowScheduleResponseCatchUp] = {
    "latest",
    "skip",
}


def check_workflow_schedule_response_catch_up(value: str) -> WorkflowScheduleResponseCatchUp:
    if value in WORKFLOW_SCHEDULE_RESPONSE_CATCH_UP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_SCHEDULE_RESPONSE_CATCH_UP_VALUES!r}")

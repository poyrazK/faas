from typing import Literal

TaskToolReleaseReceiptStage = Literal[
    "complete",
    "deploying_web",
    "deploying_worker",
    "draining_previous",
    "prepared",
    "previous_parked",
    "replacements_started",
    "web_promoted",
]

TASK_TOOL_RELEASE_RECEIPT_STAGE_VALUES: set[TaskToolReleaseReceiptStage] = {
    "complete",
    "deploying_web",
    "deploying_worker",
    "draining_previous",
    "prepared",
    "previous_parked",
    "replacements_started",
    "web_promoted",
}


def check_task_tool_release_receipt_stage(value: str) -> TaskToolReleaseReceiptStage:
    if value in TASK_TOOL_RELEASE_RECEIPT_STAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_RECEIPT_STAGE_VALUES!r}")

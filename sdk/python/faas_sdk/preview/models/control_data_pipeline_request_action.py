from typing import Literal

ControlDataPipelineRequestAction = Literal["delete", "pause", "resume", "resync", "verify"]

CONTROL_DATA_PIPELINE_REQUEST_ACTION_VALUES: set[ControlDataPipelineRequestAction] = {
    "delete",
    "pause",
    "resume",
    "resync",
    "verify",
}


def check_control_data_pipeline_request_action(value: str) -> ControlDataPipelineRequestAction:
    if value in CONTROL_DATA_PIPELINE_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONTROL_DATA_PIPELINE_REQUEST_ACTION_VALUES!r}")

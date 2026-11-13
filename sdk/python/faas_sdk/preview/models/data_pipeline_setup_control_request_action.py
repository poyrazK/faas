from typing import Literal

DataPipelineSetupControlRequestAction = Literal["cancel", "retry"]

DATA_PIPELINE_SETUP_CONTROL_REQUEST_ACTION_VALUES: set[DataPipelineSetupControlRequestAction] = {
    "cancel",
    "retry",
}


def check_data_pipeline_setup_control_request_action(value: str) -> DataPipelineSetupControlRequestAction:
    if value in DATA_PIPELINE_SETUP_CONTROL_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_SETUP_CONTROL_REQUEST_ACTION_VALUES!r}"
    )

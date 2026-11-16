from typing import Literal

DataPipelineSnapshotStatusState = Literal["due", "failed", "running", "succeeded"]

DATA_PIPELINE_SNAPSHOT_STATUS_STATE_VALUES: set[DataPipelineSnapshotStatusState] = {
    "due",
    "failed",
    "running",
    "succeeded",
}


def check_data_pipeline_snapshot_status_state(value: str) -> DataPipelineSnapshotStatusState:
    if value in DATA_PIPELINE_SNAPSHOT_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_SNAPSHOT_STATUS_STATE_VALUES!r}")

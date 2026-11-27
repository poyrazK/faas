from typing import Literal

RunArtifactDeliveryStatus = Literal["completed", "failed", "pending"]

RUN_ARTIFACT_DELIVERY_STATUS_VALUES: set[RunArtifactDeliveryStatus] = {
    "completed",
    "failed",
    "pending",
}


def check_run_artifact_delivery_status(value: str) -> RunArtifactDeliveryStatus:
    if value in RUN_ARTIFACT_DELIVERY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUN_ARTIFACT_DELIVERY_STATUS_VALUES!r}")

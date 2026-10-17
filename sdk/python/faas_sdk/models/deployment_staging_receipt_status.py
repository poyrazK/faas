from typing import Literal

DeploymentStagingReceiptStatus = Literal["activated", "cleaned", "conflict", "discarded", "prepared", "staged"]

DEPLOYMENT_STAGING_RECEIPT_STATUS_VALUES: set[DeploymentStagingReceiptStatus] = {
    "activated",
    "cleaned",
    "conflict",
    "discarded",
    "prepared",
    "staged",
}


def check_deployment_staging_receipt_status(value: str) -> DeploymentStagingReceiptStatus:
    if value in DEPLOYMENT_STAGING_RECEIPT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_STAGING_RECEIPT_STATUS_VALUES!r}")

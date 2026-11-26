from typing import Literal

DataAPIInspectionResponseBindingIdentityStatus = Literal["mismatch", "unknown", "verified"]

DATA_API_INSPECTION_RESPONSE_BINDING_IDENTITY_STATUS_VALUES: set[DataAPIInspectionResponseBindingIdentityStatus] = {
    "mismatch",
    "unknown",
    "verified",
}


def check_data_api_inspection_response_binding_identity_status(
    value: str,
) -> DataAPIInspectionResponseBindingIdentityStatus:
    if value in DATA_API_INSPECTION_RESPONSE_BINDING_IDENTITY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DATA_API_INSPECTION_RESPONSE_BINDING_IDENTITY_STATUS_VALUES!r}"
    )

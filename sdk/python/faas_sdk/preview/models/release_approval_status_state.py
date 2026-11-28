from typing import Literal

ReleaseApprovalStatusState = Literal["approved", "expired", "not_required", "rejected", "waiting"]

RELEASE_APPROVAL_STATUS_STATE_VALUES: set[ReleaseApprovalStatusState] = {
    "approved",
    "expired",
    "not_required",
    "rejected",
    "waiting",
}


def check_release_approval_status_state(value: str) -> ReleaseApprovalStatusState:
    if value in RELEASE_APPROVAL_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_APPROVAL_STATUS_STATE_VALUES!r}")

from typing import Literal

CreateReleaseFreezeRequestKind = Literal["after_rollback", "weekly", "window"]

CREATE_RELEASE_FREEZE_REQUEST_KIND_VALUES: set[CreateReleaseFreezeRequestKind] = {
    "after_rollback",
    "weekly",
    "window",
}


def check_create_release_freeze_request_kind(value: str) -> CreateReleaseFreezeRequestKind:
    if value in CREATE_RELEASE_FREEZE_REQUEST_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_RELEASE_FREEZE_REQUEST_KIND_VALUES!r}")

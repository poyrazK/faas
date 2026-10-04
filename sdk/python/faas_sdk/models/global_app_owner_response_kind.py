from typing import Literal

GlobalAppOwnerResponseKind = Literal["account", "organization"]

GLOBAL_APP_OWNER_RESPONSE_KIND_VALUES: set[GlobalAppOwnerResponseKind] = {
    "account",
    "organization",
}


def check_global_app_owner_response_kind(value: str) -> GlobalAppOwnerResponseKind:
    if value in GLOBAL_APP_OWNER_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GLOBAL_APP_OWNER_RESPONSE_KIND_VALUES!r}")

from typing import Literal

PublicAuthStatusMode = Literal["basic", "bearer", "internal_only", "ip_allowlist", "members_only", "open"]

PUBLIC_AUTH_STATUS_MODE_VALUES: set[PublicAuthStatusMode] = {
    "basic",
    "bearer",
    "internal_only",
    "ip_allowlist",
    "members_only",
    "open",
}


def check_public_auth_status_mode(value: str) -> PublicAuthStatusMode:
    if value in PUBLIC_AUTH_STATUS_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_AUTH_STATUS_MODE_VALUES!r}")

from typing import Literal

CompanionDeclarationType = Literal["init", "sidecar"]

COMPANION_DECLARATION_TYPE_VALUES: set[CompanionDeclarationType] = {
    "init",
    "sidecar",
}


def check_companion_declaration_type(value: str) -> CompanionDeclarationType:
    if value in COMPANION_DECLARATION_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {COMPANION_DECLARATION_TYPE_VALUES!r}")

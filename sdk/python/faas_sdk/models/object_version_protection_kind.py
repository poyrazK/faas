from typing import Literal

ObjectVersionProtectionKind = Literal["legal_hold", "retention"]

OBJECT_VERSION_PROTECTION_KIND_VALUES: set[ObjectVersionProtectionKind] = {
    "legal_hold",
    "retention",
}


def check_object_version_protection_kind(value: str) -> ObjectVersionProtectionKind:
    if value in OBJECT_VERSION_PROTECTION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_VERSION_PROTECTION_KIND_VALUES!r}")

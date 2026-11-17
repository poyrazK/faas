from typing import Literal

AfterRestoreHookIdentity = Literal["reload"]

AFTER_RESTORE_HOOK_IDENTITY_VALUES: set[AfterRestoreHookIdentity] = {
    "reload",
}


def check_after_restore_hook_identity(value: str) -> AfterRestoreHookIdentity:
    if value in AFTER_RESTORE_HOOK_IDENTITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AFTER_RESTORE_HOOK_IDENTITY_VALUES!r}")

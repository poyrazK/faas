from typing import Literal

ManagedPostgresMaskedColumnAction = Literal["email", "keep", "null", "token"]

MANAGED_POSTGRES_MASKED_COLUMN_ACTION_VALUES: set[ManagedPostgresMaskedColumnAction] = {
    "email",
    "keep",
    "null",
    "token",
}


def check_managed_postgres_masked_column_action(value: str) -> ManagedPostgresMaskedColumnAction:
    if value in MANAGED_POSTGRES_MASKED_COLUMN_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_MASKED_COLUMN_ACTION_VALUES!r}")

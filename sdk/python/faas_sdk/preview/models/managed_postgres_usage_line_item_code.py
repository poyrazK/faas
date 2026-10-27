from typing import Literal

ManagedPostgresUsageLineItemCode = Literal[
    "managed_postgres.compute",
    "managed_postgres.egress",
    "managed_postgres.restore_history",
    "managed_postgres.storage",
]

MANAGED_POSTGRES_USAGE_LINE_ITEM_CODE_VALUES: set[ManagedPostgresUsageLineItemCode] = {
    "managed_postgres.compute",
    "managed_postgres.egress",
    "managed_postgres.restore_history",
    "managed_postgres.storage",
}


def check_managed_postgres_usage_line_item_code(value: str) -> ManagedPostgresUsageLineItemCode:
    if value in MANAGED_POSTGRES_USAGE_LINE_ITEM_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_USAGE_LINE_ITEM_CODE_VALUES!r}")

from typing import Literal

PostgresChangeFeedOperationsItem = Literal["delete", "insert", "update"]

POSTGRES_CHANGE_FEED_OPERATIONS_ITEM_VALUES: set[PostgresChangeFeedOperationsItem] = {
    "delete",
    "insert",
    "update",
}


def check_postgres_change_feed_operations_item(value: str) -> PostgresChangeFeedOperationsItem:
    if value in POSTGRES_CHANGE_FEED_OPERATIONS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {POSTGRES_CHANGE_FEED_OPERATIONS_ITEM_VALUES!r}")

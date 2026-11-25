from typing import Literal

GetManagedPostgresTableHealthSort = Literal["dead-rows", "size", "xid-age"]

GET_MANAGED_POSTGRES_TABLE_HEALTH_SORT_VALUES: set[GetManagedPostgresTableHealthSort] = {
    "dead-rows",
    "size",
    "xid-age",
}


def check_get_managed_postgres_table_health_sort(value: str) -> GetManagedPostgresTableHealthSort:
    if value in GET_MANAGED_POSTGRES_TABLE_HEALTH_SORT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_MANAGED_POSTGRES_TABLE_HEALTH_SORT_VALUES!r}")

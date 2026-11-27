from typing import Literal

GetOrgManagedPostgresTableHealthSort = Literal["dead-rows", "size", "xid-age"]

GET_ORG_MANAGED_POSTGRES_TABLE_HEALTH_SORT_VALUES: set[GetOrgManagedPostgresTableHealthSort] = {
    "dead-rows",
    "size",
    "xid-age",
}


def check_get_org_managed_postgres_table_health_sort(value: str) -> GetOrgManagedPostgresTableHealthSort:
    if value in GET_ORG_MANAGED_POSTGRES_TABLE_HEALTH_SORT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GET_ORG_MANAGED_POSTGRES_TABLE_HEALTH_SORT_VALUES!r}"
    )

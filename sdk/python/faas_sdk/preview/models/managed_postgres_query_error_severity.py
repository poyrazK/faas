from typing import Literal

ManagedPostgresQueryErrorSeverity = Literal["ERROR", "FATAL", "PANIC"]

MANAGED_POSTGRES_QUERY_ERROR_SEVERITY_VALUES: set[ManagedPostgresQueryErrorSeverity] = {
    "ERROR",
    "FATAL",
    "PANIC",
}


def check_managed_postgres_query_error_severity(value: str) -> ManagedPostgresQueryErrorSeverity:
    if value in MANAGED_POSTGRES_QUERY_ERROR_SEVERITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_QUERY_ERROR_SEVERITY_VALUES!r}")

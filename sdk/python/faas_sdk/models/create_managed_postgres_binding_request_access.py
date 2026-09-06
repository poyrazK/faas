from typing import Literal

CreateManagedPostgresBindingRequestAccess = Literal["read_only", "read_write"]

CREATE_MANAGED_POSTGRES_BINDING_REQUEST_ACCESS_VALUES: set[CreateManagedPostgresBindingRequestAccess] = {
    "read_only",
    "read_write",
}


def check_create_managed_postgres_binding_request_access(value: str) -> CreateManagedPostgresBindingRequestAccess:
    if value in CREATE_MANAGED_POSTGRES_BINDING_REQUEST_ACCESS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_MANAGED_POSTGRES_BINDING_REQUEST_ACCESS_VALUES!r}"
    )

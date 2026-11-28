from typing import Literal

AppResourceInventoryResponseSchemaVersion = Literal[1]

APP_RESOURCE_INVENTORY_RESPONSE_SCHEMA_VERSION_VALUES: set[AppResourceInventoryResponseSchemaVersion] = {
    1,
}


def check_app_resource_inventory_response_schema_version(value: int) -> AppResourceInventoryResponseSchemaVersion:
    if value in APP_RESOURCE_INVENTORY_RESPONSE_SCHEMA_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_RESOURCE_INVENTORY_RESPONSE_SCHEMA_VERSION_VALUES!r}"
    )

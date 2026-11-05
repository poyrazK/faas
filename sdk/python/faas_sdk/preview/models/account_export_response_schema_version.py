from typing import Literal

AccountExportResponseSchemaVersion = Literal[2]

ACCOUNT_EXPORT_RESPONSE_SCHEMA_VERSION_VALUES: set[AccountExportResponseSchemaVersion] = {
    2,
}


def check_account_export_response_schema_version(value: int) -> AccountExportResponseSchemaVersion:
    if value in ACCOUNT_EXPORT_RESPONSE_SCHEMA_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ACCOUNT_EXPORT_RESPONSE_SCHEMA_VERSION_VALUES!r}")

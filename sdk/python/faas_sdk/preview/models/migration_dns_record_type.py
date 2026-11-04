from typing import Literal

MigrationDNSRecordType = Literal["A", "AAAA", "CNAME"]

MIGRATION_DNS_RECORD_TYPE_VALUES: set[MigrationDNSRecordType] = {
    "A",
    "AAAA",
    "CNAME",
}


def check_migration_dns_record_type(value: str) -> MigrationDNSRecordType:
    if value in MIGRATION_DNS_RECORD_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MIGRATION_DNS_RECORD_TYPE_VALUES!r}")

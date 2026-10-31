from typing import Literal

ManagedPostgresAccountingDiagnosticState = Literal["deleted", "deleting", "failed", "provisioning", "ready", "updating"]

MANAGED_POSTGRES_ACCOUNTING_DIAGNOSTIC_STATE_VALUES: set[ManagedPostgresAccountingDiagnosticState] = {
    "deleted",
    "deleting",
    "failed",
    "provisioning",
    "ready",
    "updating",
}


def check_managed_postgres_accounting_diagnostic_state(value: str) -> ManagedPostgresAccountingDiagnosticState:
    if value in MANAGED_POSTGRES_ACCOUNTING_DIAGNOSTIC_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_ACCOUNTING_DIAGNOSTIC_STATE_VALUES!r}"
    )

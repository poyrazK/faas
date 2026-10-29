from typing import Literal

ManagedPostgresAccountingDiagnosticReasonsItem = Literal[
    "coverage_incomplete",
    "coverage_missing",
    "final_correction_pending",
    "identity_unknown",
    "legacy_identity_unknown",
    "observation_stale",
    "shutdown_unconfirmed",
    "window_mismatch",
]

MANAGED_POSTGRES_ACCOUNTING_DIAGNOSTIC_REASONS_ITEM_VALUES: set[ManagedPostgresAccountingDiagnosticReasonsItem] = {
    "coverage_incomplete",
    "coverage_missing",
    "final_correction_pending",
    "identity_unknown",
    "legacy_identity_unknown",
    "observation_stale",
    "shutdown_unconfirmed",
    "window_mismatch",
}


def check_managed_postgres_accounting_diagnostic_reasons_item(
    value: str,
) -> ManagedPostgresAccountingDiagnosticReasonsItem:
    if value in MANAGED_POSTGRES_ACCOUNTING_DIAGNOSTIC_REASONS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_ACCOUNTING_DIAGNOSTIC_REASONS_ITEM_VALUES!r}"
    )

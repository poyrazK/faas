from typing import Literal

ManagedPostgresCutoverPreflightBlockersItem = Literal[
    "activation_disabled",
    "bindings_changed",
    "cutover_inactive",
    "resume_plan_missing",
    "routes_unconverged",
    "runtime_safety_unproven",
    "snapshot_invalidation_unproven",
    "source_changed",
    "source_sessions_unfenced",
    "target_changed",
    "verification_required",
]

MANAGED_POSTGRES_CUTOVER_PREFLIGHT_BLOCKERS_ITEM_VALUES: set[ManagedPostgresCutoverPreflightBlockersItem] = {
    "activation_disabled",
    "bindings_changed",
    "cutover_inactive",
    "resume_plan_missing",
    "routes_unconverged",
    "runtime_safety_unproven",
    "snapshot_invalidation_unproven",
    "source_changed",
    "source_sessions_unfenced",
    "target_changed",
    "verification_required",
}


def check_managed_postgres_cutover_preflight_blockers_item(value: str) -> ManagedPostgresCutoverPreflightBlockersItem:
    if value in MANAGED_POSTGRES_CUTOVER_PREFLIGHT_BLOCKERS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CUTOVER_PREFLIGHT_BLOCKERS_ITEM_VALUES!r}"
    )

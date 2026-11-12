from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_usage_response_compute_cap_state import (
    ManagedPostgresUsageResponseComputeCapState,
    check_managed_postgres_usage_response_compute_cap_state,
)
from ..models.managed_postgres_usage_response_egress_state import (
    ManagedPostgresUsageResponseEgressState,
    check_managed_postgres_usage_response_egress_state,
)
from ..models.managed_postgres_usage_response_guardrail_state import (
    ManagedPostgresUsageResponseGuardrailState,
    check_managed_postgres_usage_response_guardrail_state,
)
from ..models.managed_postgres_usage_response_hold import (
    ManagedPostgresUsageResponseHold,
    check_managed_postgres_usage_response_hold,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresUsageResponse")


@_attrs_define
class ManagedPostgresUsageResponse:
    """Customer-safe current UTC-month managed PostgreSQL usage and guardrail state."""

    period_start: datetime.datetime
    policy_enabled: bool
    fresh: bool
    guardrail_state: ManagedPostgresUsageResponseGuardrailState
    ready_databases: int
    database_limit: int
    storage_limit_bytes: int
    compute_unit_seconds: int
    storage_byte_seconds: int
    storage_byte_seconds_limit: int
    storage_byte_seconds_remaining: int
    history_byte_seconds: int
    egress_bytes: int
    observed_at: datetime.datetime | None | Unset = UNSET
    compute_allowance_unit_seconds: int | Unset = UNSET
    """The plan's monthly compute allowance in compute-unit seconds, pooled across the account's databases."""
    compute_ceiling_unit_seconds: int | Unset = UNSET
    """Compute-unit seconds this month at which running databases are suspended. Absent while the usage ledger is
    off."""
    compute_cap_state: ManagedPostgresUsageResponseComputeCapState | Unset = UNSET
    """healthy; warning at 80% of the allowance; overage past the allowance while the account allows overage;
    suspended once the ceiling is reached and the platform enforces caps. Suspended databases keep their data and
    resume next month or after an upgrade."""
    overage_enabled: bool | Unset = UNSET
    """True while the account runs its databases past the allowance, up to the ceiling."""
    compute_suspended_at: datetime.datetime | Unset = UNSET
    """When the compute cap suspended the account's databases this month; present while compute_cap_state is
    suspended."""
    egress_allowance_bytes: int | Unset = UNSET
    """Each database's own monthly egress allowance, 500 GB (10^9 bytes) as Neon includes per project. Database
    egress does not count toward the plan's app egress allowance. Absent on a plan without databases."""
    app_egress_bytes: int | Unset = UNSET
    """Deprecated and always omitted. Apps no longer share the databases' egress allowance."""
    egress_state: ManagedPostgresUsageResponseEgressState | Unset = UNSET
    """The state of the database that sent the most egress this month: healthy; warning at 80% of its hold; overage
    past its allowance while the account's egress overage election bills database egress (at Neon's price plus 5%,
    up to twice the allowance); held once it reaches its allowance (twice it with overage billed) and the platform
    enforces caps, until the month ends. Only that database is held."""
    hold: ManagedPostgresUsageResponseHold | Unset = UNSET
    """Set while every database of the account is held, with the same reason each database reports. Egress holds
    one database at a time, so it is reported on that database, not here."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        period_start = self.period_start.isoformat()

        policy_enabled = self.policy_enabled

        fresh = self.fresh

        guardrail_state: str = self.guardrail_state

        ready_databases = self.ready_databases

        database_limit = self.database_limit

        storage_limit_bytes = self.storage_limit_bytes

        compute_unit_seconds = self.compute_unit_seconds

        storage_byte_seconds = self.storage_byte_seconds

        storage_byte_seconds_limit = self.storage_byte_seconds_limit

        storage_byte_seconds_remaining = self.storage_byte_seconds_remaining

        history_byte_seconds = self.history_byte_seconds

        egress_bytes = self.egress_bytes

        observed_at: None | str | Unset
        if isinstance(self.observed_at, Unset):
            observed_at = UNSET
        elif isinstance(self.observed_at, datetime.datetime):
            observed_at = self.observed_at.isoformat()
        else:
            observed_at = self.observed_at

        compute_allowance_unit_seconds = self.compute_allowance_unit_seconds

        compute_ceiling_unit_seconds = self.compute_ceiling_unit_seconds

        compute_cap_state: str | Unset = UNSET
        if not isinstance(self.compute_cap_state, Unset):
            compute_cap_state = self.compute_cap_state

        overage_enabled = self.overage_enabled

        compute_suspended_at: str | Unset = UNSET
        if not isinstance(self.compute_suspended_at, Unset):
            compute_suspended_at = self.compute_suspended_at.isoformat()

        egress_allowance_bytes = self.egress_allowance_bytes

        app_egress_bytes = self.app_egress_bytes

        egress_state: str | Unset = UNSET
        if not isinstance(self.egress_state, Unset):
            egress_state = self.egress_state

        hold: str | Unset = UNSET
        if not isinstance(self.hold, Unset):
            hold = self.hold

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "period_start": period_start,
                "policy_enabled": policy_enabled,
                "fresh": fresh,
                "guardrail_state": guardrail_state,
                "ready_databases": ready_databases,
                "database_limit": database_limit,
                "storage_limit_bytes": storage_limit_bytes,
                "compute_unit_seconds": compute_unit_seconds,
                "storage_byte_seconds": storage_byte_seconds,
                "storage_byte_seconds_limit": storage_byte_seconds_limit,
                "storage_byte_seconds_remaining": storage_byte_seconds_remaining,
                "history_byte_seconds": history_byte_seconds,
                "egress_bytes": egress_bytes,
            }
        )
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if compute_allowance_unit_seconds is not UNSET:
            field_dict["compute_allowance_unit_seconds"] = compute_allowance_unit_seconds
        if compute_ceiling_unit_seconds is not UNSET:
            field_dict["compute_ceiling_unit_seconds"] = compute_ceiling_unit_seconds
        if compute_cap_state is not UNSET:
            field_dict["compute_cap_state"] = compute_cap_state
        if overage_enabled is not UNSET:
            field_dict["overage_enabled"] = overage_enabled
        if compute_suspended_at is not UNSET:
            field_dict["compute_suspended_at"] = compute_suspended_at
        if egress_allowance_bytes is not UNSET:
            field_dict["egress_allowance_bytes"] = egress_allowance_bytes
        if app_egress_bytes is not UNSET:
            field_dict["app_egress_bytes"] = app_egress_bytes
        if egress_state is not UNSET:
            field_dict["egress_state"] = egress_state
        if hold is not UNSET:
            field_dict["hold"] = hold

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        policy_enabled = d.pop("policy_enabled")

        fresh = d.pop("fresh")

        guardrail_state = check_managed_postgres_usage_response_guardrail_state(d.pop("guardrail_state"))

        ready_databases = d.pop("ready_databases")

        database_limit = d.pop("database_limit")

        storage_limit_bytes = d.pop("storage_limit_bytes")

        compute_unit_seconds = d.pop("compute_unit_seconds")

        storage_byte_seconds = d.pop("storage_byte_seconds")

        storage_byte_seconds_limit = d.pop("storage_byte_seconds_limit")

        storage_byte_seconds_remaining = d.pop("storage_byte_seconds_remaining")

        history_byte_seconds = d.pop("history_byte_seconds")

        egress_bytes = d.pop("egress_bytes")

        def _parse_observed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                observed_at_type_0 = datetime.datetime.fromisoformat(data)

                return observed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        observed_at = _parse_observed_at(d.pop("observed_at", UNSET))

        compute_allowance_unit_seconds = d.pop("compute_allowance_unit_seconds", UNSET)

        compute_ceiling_unit_seconds = d.pop("compute_ceiling_unit_seconds", UNSET)

        _compute_cap_state = d.pop("compute_cap_state", UNSET)
        compute_cap_state: ManagedPostgresUsageResponseComputeCapState | Unset
        if isinstance(_compute_cap_state, Unset):
            compute_cap_state = UNSET
        else:
            compute_cap_state = check_managed_postgres_usage_response_compute_cap_state(_compute_cap_state)

        overage_enabled = d.pop("overage_enabled", UNSET)

        _compute_suspended_at = d.pop("compute_suspended_at", UNSET)
        compute_suspended_at: datetime.datetime | Unset
        if isinstance(_compute_suspended_at, Unset):
            compute_suspended_at = UNSET
        else:
            compute_suspended_at = datetime.datetime.fromisoformat(_compute_suspended_at)

        egress_allowance_bytes = d.pop("egress_allowance_bytes", UNSET)

        app_egress_bytes = d.pop("app_egress_bytes", UNSET)

        _egress_state = d.pop("egress_state", UNSET)
        egress_state: ManagedPostgresUsageResponseEgressState | Unset
        if isinstance(_egress_state, Unset):
            egress_state = UNSET
        else:
            egress_state = check_managed_postgres_usage_response_egress_state(_egress_state)

        _hold = d.pop("hold", UNSET)
        hold: ManagedPostgresUsageResponseHold | Unset
        if isinstance(_hold, Unset):
            hold = UNSET
        else:
            hold = check_managed_postgres_usage_response_hold(_hold)

        managed_postgres_usage_response = cls(
            period_start=period_start,
            policy_enabled=policy_enabled,
            fresh=fresh,
            guardrail_state=guardrail_state,
            ready_databases=ready_databases,
            database_limit=database_limit,
            storage_limit_bytes=storage_limit_bytes,
            compute_unit_seconds=compute_unit_seconds,
            storage_byte_seconds=storage_byte_seconds,
            storage_byte_seconds_limit=storage_byte_seconds_limit,
            storage_byte_seconds_remaining=storage_byte_seconds_remaining,
            history_byte_seconds=history_byte_seconds,
            egress_bytes=egress_bytes,
            observed_at=observed_at,
            compute_allowance_unit_seconds=compute_allowance_unit_seconds,
            compute_ceiling_unit_seconds=compute_ceiling_unit_seconds,
            compute_cap_state=compute_cap_state,
            overage_enabled=overage_enabled,
            compute_suspended_at=compute_suspended_at,
            egress_allowance_bytes=egress_allowance_bytes,
            app_egress_bytes=app_egress_bytes,
            egress_state=egress_state,
            hold=hold,
        )

        managed_postgres_usage_response.additional_properties = d
        return managed_postgres_usage_response

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties

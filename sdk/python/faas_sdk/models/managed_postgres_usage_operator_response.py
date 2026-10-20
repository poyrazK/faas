from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_usage_operator_response_guardrail_state import (
    ManagedPostgresUsageOperatorResponseGuardrailState,
    check_managed_postgres_usage_operator_response_guardrail_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_usage_line_item import ManagedPostgresUsageLineItem


T = TypeVar("T", bound="ManagedPostgresUsageOperatorResponse")


@_attrs_define
class ManagedPostgresUsageOperatorResponse:
    """Operator-only managed PostgreSQL usage, effective ceilings, and internal COGS line items."""

    account_id: UUID
    period_start: datetime.datetime
    policy_enabled: bool
    fresh: bool
    guardrail_state: ManagedPostgresUsageOperatorResponseGuardrailState
    ready_databases: int
    database_limit: int
    storage_limit_bytes: int
    compute_unit_seconds: int
    storage_byte_seconds: int
    storage_byte_seconds_limit: int
    storage_byte_seconds_remaining: int
    history_byte_seconds: int
    egress_bytes: int
    cost_millicents: int
    max_monthly_cost_millicents: int
    max_monthly_compute_unit_seconds: int
    max_monthly_storage_byte_seconds: int
    max_monthly_history_byte_seconds: int
    max_monthly_egress_bytes: int
    line_items: list[ManagedPostgresUsageLineItem]
    observed_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

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

        cost_millicents = self.cost_millicents

        max_monthly_cost_millicents = self.max_monthly_cost_millicents

        max_monthly_compute_unit_seconds = self.max_monthly_compute_unit_seconds

        max_monthly_storage_byte_seconds = self.max_monthly_storage_byte_seconds

        max_monthly_history_byte_seconds = self.max_monthly_history_byte_seconds

        max_monthly_egress_bytes = self.max_monthly_egress_bytes

        line_items = []
        for line_items_item_data in self.line_items:
            line_items_item = line_items_item_data.to_dict()
            line_items.append(line_items_item)

        observed_at: None | str | Unset
        if isinstance(self.observed_at, Unset):
            observed_at = UNSET
        elif isinstance(self.observed_at, datetime.datetime):
            observed_at = self.observed_at.isoformat()
        else:
            observed_at = self.observed_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
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
                "cost_millicents": cost_millicents,
                "max_monthly_cost_millicents": max_monthly_cost_millicents,
                "max_monthly_compute_unit_seconds": max_monthly_compute_unit_seconds,
                "max_monthly_storage_byte_seconds": max_monthly_storage_byte_seconds,
                "max_monthly_history_byte_seconds": max_monthly_history_byte_seconds,
                "max_monthly_egress_bytes": max_monthly_egress_bytes,
                "line_items": line_items,
            }
        )
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_usage_line_item import ManagedPostgresUsageLineItem

        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        policy_enabled = d.pop("policy_enabled")

        fresh = d.pop("fresh")

        guardrail_state = check_managed_postgres_usage_operator_response_guardrail_state(d.pop("guardrail_state"))

        ready_databases = d.pop("ready_databases")

        database_limit = d.pop("database_limit")

        storage_limit_bytes = d.pop("storage_limit_bytes")

        compute_unit_seconds = d.pop("compute_unit_seconds")

        storage_byte_seconds = d.pop("storage_byte_seconds")

        storage_byte_seconds_limit = d.pop("storage_byte_seconds_limit")

        storage_byte_seconds_remaining = d.pop("storage_byte_seconds_remaining")

        history_byte_seconds = d.pop("history_byte_seconds")

        egress_bytes = d.pop("egress_bytes")

        cost_millicents = d.pop("cost_millicents")

        max_monthly_cost_millicents = d.pop("max_monthly_cost_millicents")

        max_monthly_compute_unit_seconds = d.pop("max_monthly_compute_unit_seconds")

        max_monthly_storage_byte_seconds = d.pop("max_monthly_storage_byte_seconds")

        max_monthly_history_byte_seconds = d.pop("max_monthly_history_byte_seconds")

        max_monthly_egress_bytes = d.pop("max_monthly_egress_bytes")

        line_items = []
        _line_items = d.pop("line_items")
        for line_items_item_data in _line_items:
            line_items_item = ManagedPostgresUsageLineItem.from_dict(line_items_item_data)

            line_items.append(line_items_item)

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

        managed_postgres_usage_operator_response = cls(
            account_id=account_id,
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
            cost_millicents=cost_millicents,
            max_monthly_cost_millicents=max_monthly_cost_millicents,
            max_monthly_compute_unit_seconds=max_monthly_compute_unit_seconds,
            max_monthly_storage_byte_seconds=max_monthly_storage_byte_seconds,
            max_monthly_history_byte_seconds=max_monthly_history_byte_seconds,
            max_monthly_egress_bytes=max_monthly_egress_bytes,
            line_items=line_items,
            observed_at=observed_at,
        )

        managed_postgres_usage_operator_response.additional_properties = d
        return managed_postgres_usage_operator_response

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

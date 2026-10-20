from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_usage_response_guardrail_state import (
    ManagedPostgresUsageResponseGuardrailState,
    check_managed_postgres_usage_response_guardrail_state,
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

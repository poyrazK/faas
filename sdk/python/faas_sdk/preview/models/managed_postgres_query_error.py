from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_query_error_severity import (
    ManagedPostgresQueryErrorSeverity,
    check_managed_postgres_query_error_severity,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresQueryError")


@_attrs_define
class ManagedPostgresQueryError:
    observed_at: datetime.datetime
    severity: ManagedPostgresQueryErrorSeverity
    sqlstate: str
    binding_id: UUID | Unset = UNSET
    """App binding when available."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        severity: str = self.severity

        sqlstate = self.sqlstate

        binding_id: str | Unset = UNSET
        if not isinstance(self.binding_id, Unset):
            binding_id = str(self.binding_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observed_at": observed_at,
                "severity": severity,
                "sqlstate": sqlstate,
            }
        )
        if binding_id is not UNSET:
            field_dict["binding_id"] = binding_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        severity = check_managed_postgres_query_error_severity(d.pop("severity"))

        sqlstate = d.pop("sqlstate")

        _binding_id = d.pop("binding_id", UNSET)
        binding_id: UUID | Unset
        if isinstance(_binding_id, Unset):
            binding_id = UNSET
        else:
            binding_id = UUID(_binding_id)

        managed_postgres_query_error = cls(
            observed_at=observed_at,
            severity=severity,
            sqlstate=sqlstate,
            binding_id=binding_id,
        )

        managed_postgres_query_error.additional_properties = d
        return managed_postgres_query_error

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

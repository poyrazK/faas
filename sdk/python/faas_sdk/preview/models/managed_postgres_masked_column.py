from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.managed_postgres_masked_column_action import (
    ManagedPostgresMaskedColumnAction,
    check_managed_postgres_masked_column_action,
)

T = TypeVar("T", bound="ManagedPostgresMaskedColumn")


@_attrs_define
class ManagedPostgresMaskedColumn:
    name: str
    action: ManagedPostgresMaskedColumnAction
    """How values in this selected column are copied."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        action: str = self.action

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "action": action,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        action = check_managed_postgres_masked_column_action(d.pop("action"))

        managed_postgres_masked_column = cls(
            name=name,
            action=action,
        )

        return managed_postgres_masked_column

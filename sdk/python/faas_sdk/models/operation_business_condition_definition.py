from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationBusinessConditionDefinition")


@_attrs_define
class OperationBusinessConditionDefinition:
    """Public condition vocabulary declared on a pinned workflow milestone step. A definition does not evaluate a
    predicate.

    """

    id: str
    description: str
    """Public UTF-8 explanation without control characters; byte bound."""

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        description = self.description

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "description": description,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        description = d.pop("description")

        operation_business_condition_definition = cls(
            id=id,
            description=description,
        )

        return operation_business_condition_definition

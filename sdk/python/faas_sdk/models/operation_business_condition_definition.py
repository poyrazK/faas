from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationBusinessConditionDefinition")


@_attrs_define
class OperationBusinessConditionDefinition:
    """Public condition vocabulary declared on a pinned workflow milestone step. A definition does not evaluate a
    predicate.

    """

    id: str
    description: str
    """Public UTF-8 explanation without control characters; byte bound."""
    evidence_max_age_seconds: int | Unset = UNSET
    """Optional pinned maximum condition report age in seconds. Zero or omission disables the budget; this measures
    reporting freshness without changing the application result."""

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        description = self.description

        evidence_max_age_seconds = self.evidence_max_age_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "description": description,
            }
        )
        if evidence_max_age_seconds is not UNSET:
            field_dict["evidence_max_age_seconds"] = evidence_max_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        description = d.pop("description")

        evidence_max_age_seconds = d.pop("evidence_max_age_seconds", UNSET)

        operation_business_condition_definition = cls(
            id=id,
            description=description,
            evidence_max_age_seconds=evidence_max_age_seconds,
        )

        return operation_business_condition_definition

from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_business_effect import OperationBusinessEffect


T = TypeVar("T", bound="OperationWorkflowPlannedEffect")


@_attrs_define
class OperationWorkflowPlannedEffect:
    """Proposed business effect confirmation associated with a milestone in the evidence plan."""

    milestone: str
    effect: OperationBusinessEffect
    """Application-reported effect. Text bounds are UTF-8 bytes; confirmed status requires a nonempty reference.
    Amount/currency are supplied together in minor units."""

    def to_dict(self) -> dict[str, Any]:
        milestone = self.milestone

        effect = self.effect.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "milestone": milestone,
                "effect": effect,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_effect import OperationBusinessEffect

        d = dict(src_dict)
        milestone = d.pop("milestone")

        effect = OperationBusinessEffect.from_dict(d.pop("effect"))

        operation_workflow_planned_effect = cls(
            milestone=milestone,
            effect=effect,
        )

        return operation_workflow_planned_effect

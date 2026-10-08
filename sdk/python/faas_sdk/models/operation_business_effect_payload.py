from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_business_effect_payload_kind import (
    OperationBusinessEffectPayloadKind,
    check_operation_business_effect_payload_kind,
)

if TYPE_CHECKING:
    from ..models.operation_business_effect import OperationBusinessEffect


T = TypeVar("T", bound="OperationBusinessEffectPayload")


@_attrs_define
class OperationBusinessEffectPayload:
    kind: OperationBusinessEffectPayloadKind
    effect: OperationBusinessEffect
    """Application-reported effect. Text bounds are UTF-8 bytes; confirmed status requires a nonempty reference.
    Amount/currency are supplied together in minor units."""

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        effect = self.effect.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "effect": effect,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_effect import OperationBusinessEffect

        d = dict(src_dict)
        kind = check_operation_business_effect_payload_kind(d.pop("kind"))

        effect = OperationBusinessEffect.from_dict(d.pop("effect"))

        operation_business_effect_payload = cls(
            kind=kind,
            effect=effect,
        )

        return operation_business_effect_payload

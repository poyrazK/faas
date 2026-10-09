from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_business_invariant_payload_kind import (
    OperationBusinessInvariantPayloadKind,
    check_operation_business_invariant_payload_kind,
)

if TYPE_CHECKING:
    from ..models.operation_business_invariant import OperationBusinessInvariant


T = TypeVar("T", bound="OperationBusinessInvariantPayload")


@_attrs_define
class OperationBusinessInvariantPayload:
    """Typed milestone envelope containing an application check of a business invariant."""

    kind: OperationBusinessInvariantPayloadKind
    invariant: OperationBusinessInvariant
    """Application-evaluated business condition. String limits are UTF-8 bytes; instance, version, and description
    must be nonempty without control characters."""

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        invariant = self.invariant.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "invariant": invariant,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_invariant import OperationBusinessInvariant

        d = dict(src_dict)
        kind = check_operation_business_invariant_payload_kind(d.pop("kind"))

        invariant = OperationBusinessInvariant.from_dict(d.pop("invariant"))

        operation_business_invariant_payload = cls(
            kind=kind,
            invariant=invariant,
        )

        return operation_business_invariant_payload

from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_business_compensation_payload_kind import (
    OperationBusinessCompensationPayloadKind,
    check_operation_business_compensation_payload_kind,
)

if TYPE_CHECKING:
    from ..models.operation_business_compensation import OperationBusinessCompensation


T = TypeVar("T", bound="OperationBusinessCompensationPayload")


@_attrs_define
class OperationBusinessCompensationPayload:
    """Typed milestone envelope for an application-reported compensation linked to its original effect."""

    kind: OperationBusinessCompensationPayloadKind
    compensation: OperationBusinessCompensation
    """Application-reported compensation workflow observation. Confirmed status requires a nonempty reference. Text
    bounds are UTF-8 bytes without control characters. Source must be a retained confirmed effect in the same
    account, app, customer, and environment."""

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        compensation = self.compensation.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "compensation": compensation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_compensation import OperationBusinessCompensation

        d = dict(src_dict)
        kind = check_operation_business_compensation_payload_kind(d.pop("kind"))

        compensation = OperationBusinessCompensation.from_dict(d.pop("compensation"))

        operation_business_compensation_payload = cls(
            kind=kind,
            compensation=compensation,
        )

        return operation_business_compensation_payload

from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationSubject")


@_attrs_define
class OperationSubject:
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and redeploy.
    Never an ownership or authorization claim.

    """

    type_: str
    id: str
    """Opaque application identifier, compared exactly. Maximum 256 UTF-8 bytes; ASCII controls are rejected. Use a
    public stable ID appropriate for customer-visible history."""

    def to_dict(self) -> dict[str, Any]:
        type_ = self.type_

        id = self.id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "type": type_,
                "id": id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        type_ = d.pop("type")

        id = d.pop("id")

        operation_subject = cls(
            type_=type_,
            id=id,
        )

        return operation_subject

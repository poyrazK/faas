from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.object_version_legal_hold_status import (
    ObjectVersionLegalHoldStatus,
    check_object_version_legal_hold_status,
)

T = TypeVar("T", bound="ObjectVersionLegalHold")


@_attrs_define
class ObjectVersionLegalHold:
    """Independent exact-version legal hold status."""

    status: ObjectVersionLegalHoldStatus

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_object_version_legal_hold_status(d.pop("status"))

        object_version_legal_hold = cls(
            status=status,
        )

        return object_version_legal_hold

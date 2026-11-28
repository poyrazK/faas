from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="RestoreAppBody")


@_attrs_define
class RestoreAppBody:
    expected_app_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        expected_app_id: str | Unset = UNSET
        if not isinstance(self.expected_app_id, Unset):
            expected_app_id = str(self.expected_app_id)

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if expected_app_id is not UNSET:
            field_dict["expected_app_id"] = expected_app_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _expected_app_id = d.pop("expected_app_id", UNSET)
        expected_app_id: UUID | Unset
        if isinstance(_expected_app_id, Unset):
            expected_app_id = UNSET
        else:
            expected_app_id = UUID(_expected_app_id)

        restore_app_body = cls(
            expected_app_id=expected_app_id,
        )

        return restore_app_body

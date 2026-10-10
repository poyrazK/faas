from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ParkAppBody")


@_attrs_define
class ParkAppBody:
    expected_deployment_id: UUID | Unset = UNSET
    """Compare the latest app deployment atomically with parking. A changed or missing deployment returns 409
    without parking; resend the same guard on drain retries."""

    def to_dict(self) -> dict[str, Any]:
        expected_deployment_id: str | Unset = UNSET
        if not isinstance(self.expected_deployment_id, Unset):
            expected_deployment_id = str(self.expected_deployment_id)

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if expected_deployment_id is not UNSET:
            field_dict["expected_deployment_id"] = expected_deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _expected_deployment_id = d.pop("expected_deployment_id", UNSET)
        expected_deployment_id: UUID | Unset
        if isinstance(_expected_deployment_id, Unset):
            expected_deployment_id = UNSET
        else:
            expected_deployment_id = UUID(_expected_deployment_id)

        park_app_body = cls(
            expected_deployment_id=expected_deployment_id,
        )

        return park_app_body

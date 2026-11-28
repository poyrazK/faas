from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="ParkAppIfDeploymentBody")


@_attrs_define
class ParkAppIfDeploymentBody:
    expected_deployment_id: UUID
    """Compare the latest app deployment atomically with parking. A changed or missing deployment returns 409
    without parking; resend the same guard on drain retries."""

    def to_dict(self) -> dict[str, Any]:
        expected_deployment_id = str(self.expected_deployment_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_deployment_id": expected_deployment_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_deployment_id = UUID(d.pop("expected_deployment_id"))

        park_app_if_deployment_body = cls(
            expected_deployment_id=expected_deployment_id,
        )

        return park_app_if_deployment_body

from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CheckRouteRequirementsRequest")


@_attrs_define
class CheckRouteRequirementsRequest:
    """App-owned deployment capture to evaluate against saved route intent, with an optional intent revision pin."""

    deployment_id: UUID
    expected_revision: int | Unset = UNSET
    """Optional saved revision pin; a changed revision returns 409."""

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        expected_revision = self.expected_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "deployment_id": deployment_id,
            }
        )
        if expected_revision is not UNSET:
            field_dict["expected_revision"] = expected_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        expected_revision = d.pop("expected_revision", UNSET)

        check_route_requirements_request = cls(
            deployment_id=deployment_id,
            expected_revision=expected_revision,
        )

        return check_route_requirements_request

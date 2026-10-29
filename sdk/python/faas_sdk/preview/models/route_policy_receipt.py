from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.route_policy_applied_change import RoutePolicyAppliedChange
    from ..models.route_requirements_report import RouteRequirementsReport


T = TypeVar("T", bound="RoutePolicyReceipt")


@_attrs_define
class RoutePolicyReceipt:
    """Durable historical transaction outcome, verified configuration, and actual rule identifiers."""

    id: UUID
    app_id: UUID
    plan_sha256: str
    applied_at: datetime.datetime
    changes: list[RoutePolicyAppliedChange]
    verification: RouteRequirementsReport
    """Configuration evidence for concrete requests or every captured operation with group assignments and bounded
    policy scope."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        plan_sha256 = self.plan_sha256

        applied_at = self.applied_at.isoformat()

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        verification = self.verification.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "plan_sha256": plan_sha256,
                "applied_at": applied_at,
                "changes": changes,
                "verification": verification,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_policy_applied_change import RoutePolicyAppliedChange
        from ..models.route_requirements_report import RouteRequirementsReport

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        plan_sha256 = d.pop("plan_sha256")

        applied_at = datetime.datetime.fromisoformat(d.pop("applied_at"))

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = RoutePolicyAppliedChange.from_dict(changes_item_data)

            changes.append(changes_item)

        verification = RouteRequirementsReport.from_dict(d.pop("verification"))

        route_policy_receipt = cls(
            id=id,
            app_id=app_id,
            plan_sha256=plan_sha256,
            applied_at=applied_at,
            changes=changes,
            verification=verification,
        )

        route_policy_receipt.additional_properties = d
        return route_policy_receipt

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties

from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_operational_summary_version import AppOperationalSummaryVersion, check_app_operational_summary_version

if TYPE_CHECKING:
    from ..models.app_operational_monitoring import AppOperationalMonitoring
    from ..models.app_operational_recommendation import AppOperationalRecommendation
    from ..models.app_operational_recovery import AppOperationalRecovery


T = TypeVar("T", bound="AppOperationalSummary")


@_attrs_define
class AppOperationalSummary:
    """Read-only current production route health, open incident metadata and pending recovery work, shared by inspect and
    the app dashboard.

    """

    version: AppOperationalSummaryVersion
    app_id: str
    checked_at: datetime.datetime
    monitoring: AppOperationalMonitoring
    """Recent default-scope route-monitor evaluation with observed-only coverage. Unknown or unavailable health is
    independent of deployment smoke verification."""
    recovery: AppOperationalRecovery
    """Bounded app-scoped pending rollbacks and pending or failed restart handoffs. Availability and truncation are
    explicit; accepted work does not establish completed recovery."""
    recommendations: list[AppOperationalRecommendation]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        app_id = self.app_id

        checked_at = self.checked_at.isoformat()

        monitoring = self.monitoring.to_dict()

        recovery = self.recovery.to_dict()

        recommendations = []
        for recommendations_item_data in self.recommendations:
            recommendations_item = recommendations_item_data.to_dict()
            recommendations.append(recommendations_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "app_id": app_id,
                "checked_at": checked_at,
                "monitoring": monitoring,
                "recovery": recovery,
                "recommendations": recommendations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_operational_monitoring import AppOperationalMonitoring
        from ..models.app_operational_recommendation import AppOperationalRecommendation
        from ..models.app_operational_recovery import AppOperationalRecovery

        d = dict(src_dict)
        version = check_app_operational_summary_version(d.pop("version"))

        app_id = d.pop("app_id")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        monitoring = AppOperationalMonitoring.from_dict(d.pop("monitoring"))

        recovery = AppOperationalRecovery.from_dict(d.pop("recovery"))

        recommendations = []
        _recommendations = d.pop("recommendations")
        for recommendations_item_data in _recommendations:
            recommendations_item = AppOperationalRecommendation.from_dict(recommendations_item_data)

            recommendations.append(recommendations_item)

        app_operational_summary = cls(
            version=version,
            app_id=app_id,
            checked_at=checked_at,
            monitoring=monitoring,
            recovery=recovery,
            recommendations=recommendations,
        )

        app_operational_summary.additional_properties = d
        return app_operational_summary

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

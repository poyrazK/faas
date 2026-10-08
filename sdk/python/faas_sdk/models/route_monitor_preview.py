from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.route_monitor_report import RouteMonitorReport


T = TypeVar("T", bound="RouteMonitorPreview")


@_attrs_define
class RouteMonitorPreview:
    """Read-only proposed-budget evaluation. config_change_resets_observation_anchor is true when saving the proposal would
    change monitor intent and start a fresh observation anchor; unchanged intent preserves its current anchor.

    """

    current_revision: int
    preview_only: bool
    config_change_resets_observation_anchor: bool
    report: RouteMonitorReport
    """Read-only absolute-budget evidence for the sole fully serving default-scope deployment. Coverage is limited
    to stored telemetry."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        current_revision = self.current_revision

        preview_only = self.preview_only

        config_change_resets_observation_anchor = self.config_change_resets_observation_anchor

        report = self.report.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "current_revision": current_revision,
                "preview_only": preview_only,
                "config_change_resets_observation_anchor": config_change_resets_observation_anchor,
                "report": report,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_report import RouteMonitorReport

        d = dict(src_dict)
        current_revision = d.pop("current_revision")

        preview_only = d.pop("preview_only")

        config_change_resets_observation_anchor = d.pop("config_change_resets_observation_anchor")

        report = RouteMonitorReport.from_dict(d.pop("report"))

        route_monitor_preview = cls(
            current_revision=current_revision,
            preview_only=preview_only,
            config_change_resets_observation_anchor=config_change_resets_observation_anchor,
            report=report,
        )

        route_monitor_preview.additional_properties = d
        return route_monitor_preview

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

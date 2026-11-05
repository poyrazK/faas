from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.public_status_overview_data_status import (
    PublicStatusOverviewDataStatus,
    check_public_status_overview_data_status,
)
from ..models.public_status_overview_overall_status import (
    PublicStatusOverviewOverallStatus,
    check_public_status_overview_overall_status,
)
from ..models.public_status_overview_region_scope import (
    PublicStatusOverviewRegionScope,
    check_public_status_overview_region_scope,
)

if TYPE_CHECKING:
    from ..models.public_status_component import PublicStatusComponent
    from ..models.public_status_event import PublicStatusEvent
    from ..models.public_status_indicator import PublicStatusIndicator


T = TypeVar("T", bound="PublicStatusOverview")


@_attrs_define
class PublicStatusOverview:
    """Complete public status snapshot for the single Gregale region."""

    overall_status: PublicStatusOverviewOverallStatus
    data_status: PublicStatusOverviewDataStatus
    updated_at: datetime.datetime
    region_scope: PublicStatusOverviewRegionScope
    components: list[PublicStatusComponent]
    indicators: list[PublicStatusIndicator]
    active_events: list[PublicStatusEvent]
    upcoming_maintenance: list[PublicStatusEvent]
    resolved_incidents: list[PublicStatusEvent]

    def to_dict(self) -> dict[str, Any]:
        overall_status: str = self.overall_status

        data_status: str = self.data_status

        updated_at = self.updated_at.isoformat()

        region_scope: str = self.region_scope

        components = []
        for components_item_data in self.components:
            components_item = components_item_data.to_dict()
            components.append(components_item)

        indicators = []
        for indicators_item_data in self.indicators:
            indicators_item = indicators_item_data.to_dict()
            indicators.append(indicators_item)

        active_events = []
        for active_events_item_data in self.active_events:
            active_events_item = active_events_item_data.to_dict()
            active_events.append(active_events_item)

        upcoming_maintenance = []
        for upcoming_maintenance_item_data in self.upcoming_maintenance:
            upcoming_maintenance_item = upcoming_maintenance_item_data.to_dict()
            upcoming_maintenance.append(upcoming_maintenance_item)

        resolved_incidents = []
        for resolved_incidents_item_data in self.resolved_incidents:
            resolved_incidents_item = resolved_incidents_item_data.to_dict()
            resolved_incidents.append(resolved_incidents_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "overall_status": overall_status,
                "data_status": data_status,
                "updated_at": updated_at,
                "region_scope": region_scope,
                "components": components,
                "indicators": indicators,
                "active_events": active_events,
                "upcoming_maintenance": upcoming_maintenance,
                "resolved_incidents": resolved_incidents,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.public_status_component import PublicStatusComponent
        from ..models.public_status_event import PublicStatusEvent
        from ..models.public_status_indicator import PublicStatusIndicator

        d = dict(src_dict)
        overall_status = check_public_status_overview_overall_status(d.pop("overall_status"))

        data_status = check_public_status_overview_data_status(d.pop("data_status"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        region_scope = check_public_status_overview_region_scope(d.pop("region_scope"))

        components = []
        _components = d.pop("components")
        for components_item_data in _components:
            components_item = PublicStatusComponent.from_dict(components_item_data)

            components.append(components_item)

        indicators = []
        _indicators = d.pop("indicators")
        for indicators_item_data in _indicators:
            indicators_item = PublicStatusIndicator.from_dict(indicators_item_data)

            indicators.append(indicators_item)

        active_events = []
        _active_events = d.pop("active_events")
        for active_events_item_data in _active_events:
            active_events_item = PublicStatusEvent.from_dict(active_events_item_data)

            active_events.append(active_events_item)

        upcoming_maintenance = []
        _upcoming_maintenance = d.pop("upcoming_maintenance")
        for upcoming_maintenance_item_data in _upcoming_maintenance:
            upcoming_maintenance_item = PublicStatusEvent.from_dict(upcoming_maintenance_item_data)

            upcoming_maintenance.append(upcoming_maintenance_item)

        resolved_incidents = []
        _resolved_incidents = d.pop("resolved_incidents")
        for resolved_incidents_item_data in _resolved_incidents:
            resolved_incidents_item = PublicStatusEvent.from_dict(resolved_incidents_item_data)

            resolved_incidents.append(resolved_incidents_item)

        public_status_overview = cls(
            overall_status=overall_status,
            data_status=data_status,
            updated_at=updated_at,
            region_scope=region_scope,
            components=components,
            indicators=indicators,
            active_events=active_events,
            upcoming_maintenance=upcoming_maintenance,
            resolved_incidents=resolved_incidents,
        )

        return public_status_overview

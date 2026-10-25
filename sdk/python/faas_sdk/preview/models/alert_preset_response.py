from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.alert_preset_response_category import AlertPresetResponseCategory, check_alert_preset_response_category
from ..models.alert_preset_response_comparison import (
    AlertPresetResponseComparison,
    check_alert_preset_response_comparison,
)
from ..models.alert_preset_response_minimum_plan import (
    AlertPresetResponseMinimumPlan,
    check_alert_preset_response_minimum_plan,
)
from ..models.alert_preset_response_window_spec import (
    AlertPresetResponseWindowSpec,
    check_alert_preset_response_window_spec,
)

T = TypeVar("T", bound="AlertPresetResponse")


@_attrs_define
class AlertPresetResponse:
    """One row of the alert-preset catalog (issue #1233, ADR-123).
    The catalog is system-seeded and R/O for customers — the
    enable endpoint clones the row into a real alert_rules row
    the customer owns from then on. No persistent preset_id FK
    lands on alert_rules.

    """

    id: UUID
    name: str
    display_name: str
    description: str
    category: AlertPresetResponseCategory
    metric: str
    comparison: AlertPresetResponseComparison
    threshold: float
    window_spec: AlertPresetResponseWindowSpec
    default_cooldown_minutes: int
    minimum_plan: AlertPresetResponseMinimumPlan
    enabled_in_catalog: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        display_name = self.display_name

        description = self.description

        category: str = self.category

        metric = self.metric

        comparison: str = self.comparison

        threshold = self.threshold

        window_spec: str = self.window_spec

        default_cooldown_minutes = self.default_cooldown_minutes

        minimum_plan: str = self.minimum_plan

        enabled_in_catalog = self.enabled_in_catalog

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "display_name": display_name,
                "description": description,
                "category": category,
                "metric": metric,
                "comparison": comparison,
                "threshold": threshold,
                "window_spec": window_spec,
                "default_cooldown_minutes": default_cooldown_minutes,
                "minimum_plan": minimum_plan,
                "enabled_in_catalog": enabled_in_catalog,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        display_name = d.pop("display_name")

        description = d.pop("description")

        category = check_alert_preset_response_category(d.pop("category"))

        metric = d.pop("metric")

        comparison = check_alert_preset_response_comparison(d.pop("comparison"))

        threshold = d.pop("threshold")

        window_spec = check_alert_preset_response_window_spec(d.pop("window_spec"))

        default_cooldown_minutes = d.pop("default_cooldown_minutes")

        minimum_plan = check_alert_preset_response_minimum_plan(d.pop("minimum_plan"))

        enabled_in_catalog = d.pop("enabled_in_catalog")

        alert_preset_response = cls(
            id=id,
            name=name,
            display_name=display_name,
            description=description,
            category=category,
            metric=metric,
            comparison=comparison,
            threshold=threshold,
            window_spec=window_spec,
            default_cooldown_minutes=default_cooldown_minutes,
            minimum_plan=minimum_plan,
            enabled_in_catalog=enabled_in_catalog,
        )

        alert_preset_response.additional_properties = d
        return alert_preset_response

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

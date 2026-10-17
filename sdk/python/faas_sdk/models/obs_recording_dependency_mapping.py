from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_recording_dependency_mapping_state import (
    ObsRecordingDependencyMappingState,
    check_obs_recording_dependency_mapping_state,
)

if TYPE_CHECKING:
    from ..models.obs_recording_alert_references import ObsRecordingAlertReferences


T = TypeVar("T", bound="ObsRecordingDependencyMapping")


@_attrs_define
class ObsRecordingDependencyMapping:
    """Expected catalog reference completeness; partial means some selected alerts have unresolved syntactic recording
    dependencies.

    """

    scope: Literal["selected_expected_catalog_syntax"]
    state: ObsRecordingDependencyMappingState
    alert_set_revision: str
    total_alerts: int
    uncertain_alerts: ObsRecordingAlertReferences
    """Complete count of this known catalog reference list before its five-item preview; unresolved dependencies
    can remain outside a dependent-alert list."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        state: str = self.state

        alert_set_revision = self.alert_set_revision

        total_alerts = self.total_alerts

        uncertain_alerts = self.uncertain_alerts.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "state": state,
                "alert_set_revision": alert_set_revision,
                "total_alerts": total_alerts,
                "uncertain_alerts": uncertain_alerts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_recording_alert_references import ObsRecordingAlertReferences

        d = dict(src_dict)
        scope = cast(Literal["selected_expected_catalog_syntax"], d.pop("scope"))
        if scope != "selected_expected_catalog_syntax":
            raise ValueError(f"scope must match const 'selected_expected_catalog_syntax', got '{scope}'")

        state = check_obs_recording_dependency_mapping_state(d.pop("state"))

        alert_set_revision = d.pop("alert_set_revision")

        total_alerts = d.pop("total_alerts")

        uncertain_alerts = ObsRecordingAlertReferences.from_dict(d.pop("uncertain_alerts"))

        obs_recording_dependency_mapping = cls(
            scope=scope,
            state=state,
            alert_set_revision=alert_set_revision,
            total_alerts=total_alerts,
            uncertain_alerts=uncertain_alerts,
        )

        obs_recording_dependency_mapping.additional_properties = d
        return obs_recording_dependency_mapping

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

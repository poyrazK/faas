from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.obs_alert_triage import ObsAlertTriage


T = TypeVar("T", bound="ObsAlertTriageResponse")


@_attrs_define
class ObsAlertTriageResponse:
    """Committed triage revision and workflow snapshot returned to an operator."""

    incident_id: str
    triage: ObsAlertTriage
    """Human workflow on one retained alert episode, independent of observed alert status."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        incident_id = self.incident_id

        triage = self.triage.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "incident_id": incident_id,
                "triage": triage,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_alert_triage import ObsAlertTriage

        d = dict(src_dict)
        incident_id = d.pop("incident_id")

        triage = ObsAlertTriage.from_dict(d.pop("triage"))

        obs_alert_triage_response = cls(
            incident_id=incident_id,
            triage=triage,
        )

        obs_alert_triage_response.additional_properties = d
        return obs_alert_triage_response

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

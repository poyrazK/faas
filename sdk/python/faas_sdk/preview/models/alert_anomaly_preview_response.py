from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.alert_anomaly_preview_response_step_seconds import (
    AlertAnomalyPreviewResponseStepSeconds,
    check_alert_anomaly_preview_response_step_seconds,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.alert_anomaly_incident_replay import AlertAnomalyIncidentReplay
    from ..models.alert_anomaly_preview_breach import AlertAnomalyPreviewBreach
    from ..models.alert_anomaly_preview_candidate import AlertAnomalyPreviewCandidate
    from ..models.alert_anomaly_preview_skipped_window import AlertAnomalyPreviewSkippedWindow


T = TypeVar("T", bound="AlertAnomalyPreviewResponse")


@_attrs_define
class AlertAnomalyPreviewResponse:
    """Historical anomaly evaluations with coverage, breaches, and optional replay results."""

    first_check_at: datetime.datetime
    last_check_at: datetime.datetime
    step_seconds: AlertAnomalyPreviewResponseStepSeconds
    evaluated: int
    """Windows with enough data to evaluate"""
    skipped: int
    """Checks with missing telemetry, insufficient baseline history, or too few eligible requests."""
    skipped_windows: list[AlertAnomalyPreviewSkippedWindow]
    breaches: list[AlertAnomalyPreviewBreach]
    environment_scope: str | Unset = UNSET
    """Environment used for both observed traffic and historical baselines. Present for environment-specific
    replays."""
    environment_scopes: list[str] | Unset = UNSET
    """Environments available for incident replay. Present in the hourly overview. Recent telemetry may still be
    unavailable."""
    candidate: AlertAnomalyPreviewCandidate | Unset = UNSET
    """Alternate threshold or baseline evaluated on the preview checkpoints."""
    incident_replay: AlertAnomalyIncidentReplay | Unset = UNSET
    """Anomaly incidents reconstructed from a selected two-hour interval."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        first_check_at = self.first_check_at.isoformat()

        last_check_at = self.last_check_at.isoformat()

        step_seconds: int = self.step_seconds

        evaluated = self.evaluated

        skipped = self.skipped

        skipped_windows = []
        for skipped_windows_item_data in self.skipped_windows:
            skipped_windows_item = skipped_windows_item_data.to_dict()
            skipped_windows.append(skipped_windows_item)

        breaches = []
        for breaches_item_data in self.breaches:
            breaches_item = breaches_item_data.to_dict()
            breaches.append(breaches_item)

        environment_scope = self.environment_scope

        environment_scopes: list[str] | Unset = UNSET
        if not isinstance(self.environment_scopes, Unset):
            environment_scopes = self.environment_scopes

        candidate: dict[str, Any] | Unset = UNSET
        if not isinstance(self.candidate, Unset):
            candidate = self.candidate.to_dict()

        incident_replay: dict[str, Any] | Unset = UNSET
        if not isinstance(self.incident_replay, Unset):
            incident_replay = self.incident_replay.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "first_check_at": first_check_at,
                "last_check_at": last_check_at,
                "step_seconds": step_seconds,
                "evaluated": evaluated,
                "skipped": skipped,
                "skipped_windows": skipped_windows,
                "breaches": breaches,
            }
        )
        if environment_scope is not UNSET:
            field_dict["environment_scope"] = environment_scope
        if environment_scopes is not UNSET:
            field_dict["environment_scopes"] = environment_scopes
        if candidate is not UNSET:
            field_dict["candidate"] = candidate
        if incident_replay is not UNSET:
            field_dict["incident_replay"] = incident_replay

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.alert_anomaly_incident_replay import AlertAnomalyIncidentReplay
        from ..models.alert_anomaly_preview_breach import AlertAnomalyPreviewBreach
        from ..models.alert_anomaly_preview_candidate import AlertAnomalyPreviewCandidate
        from ..models.alert_anomaly_preview_skipped_window import AlertAnomalyPreviewSkippedWindow

        d = dict(src_dict)
        first_check_at = datetime.datetime.fromisoformat(d.pop("first_check_at"))

        last_check_at = datetime.datetime.fromisoformat(d.pop("last_check_at"))

        step_seconds = check_alert_anomaly_preview_response_step_seconds(d.pop("step_seconds"))

        evaluated = d.pop("evaluated")

        skipped = d.pop("skipped")

        skipped_windows = []
        _skipped_windows = d.pop("skipped_windows")
        for skipped_windows_item_data in _skipped_windows:
            skipped_windows_item = AlertAnomalyPreviewSkippedWindow.from_dict(skipped_windows_item_data)

            skipped_windows.append(skipped_windows_item)

        breaches = []
        _breaches = d.pop("breaches")
        for breaches_item_data in _breaches:
            breaches_item = AlertAnomalyPreviewBreach.from_dict(breaches_item_data)

            breaches.append(breaches_item)

        environment_scope = d.pop("environment_scope", UNSET)

        environment_scopes = cast(list[str], d.pop("environment_scopes", UNSET))

        _candidate = d.pop("candidate", UNSET)
        candidate: AlertAnomalyPreviewCandidate | Unset
        if isinstance(_candidate, Unset):
            candidate = UNSET
        else:
            candidate = AlertAnomalyPreviewCandidate.from_dict(_candidate)

        _incident_replay = d.pop("incident_replay", UNSET)
        incident_replay: AlertAnomalyIncidentReplay | Unset
        if isinstance(_incident_replay, Unset):
            incident_replay = UNSET
        else:
            incident_replay = AlertAnomalyIncidentReplay.from_dict(_incident_replay)

        alert_anomaly_preview_response = cls(
            first_check_at=first_check_at,
            last_check_at=last_check_at,
            step_seconds=step_seconds,
            evaluated=evaluated,
            skipped=skipped,
            skipped_windows=skipped_windows,
            breaches=breaches,
            environment_scope=environment_scope,
            environment_scopes=environment_scopes,
            candidate=candidate,
            incident_replay=incident_replay,
        )

        alert_anomaly_preview_response.additional_properties = d
        return alert_anomaly_preview_response

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

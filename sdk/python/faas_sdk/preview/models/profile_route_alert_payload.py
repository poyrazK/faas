from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_route_alert_payload_source import (
    ProfileRouteAlertPayloadSource,
    check_profile_route_alert_payload_source,
)
from ..models.profile_route_alert_payload_status import (
    ProfileRouteAlertPayloadStatus,
    check_profile_route_alert_payload_status,
)
from ..models.profile_route_alert_payload_version import (
    ProfileRouteAlertPayloadVersion,
    check_profile_route_alert_payload_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_call_path_frame import ProfileCallPathFrame
    from ..models.profile_query import ProfileQuery
    from ..models.profile_route_regression import ProfileRouteRegression


T = TypeVar("T", bound="ProfileRouteAlertPayload")


@_attrs_define
class ProfileRouteAlertPayload:
    """Advisory app webhook evidence. Application frames are aggregate hotspots and do not establish route-level code
    causality. Paths are relative to Gregale; raw profile samples are excluded.

    """

    version: ProfileRouteAlertPayloadVersion
    app_id: UUID
    deployment_id: UUID
    baseline_deployment_id: UUID
    policy_revision: int
    incident_id: UUID
    status: ProfileRouteAlertPayloadStatus
    source: ProfileRouteAlertPayloadSource
    checked_at: datetime.datetime
    baseline: ProfileQuery
    """Authorized deployment CPU capture window."""
    candidate: ProfileQuery
    """Authorized deployment CPU capture window."""
    route_check: ProfileRouteRegression
    """Retained advisory route-associated sampled CPU/request observation. Missing attribution, sparse requests or
    inadequate capture coverage produces insufficient_data. No rollout decision is changed."""
    evidence_path: str
    comparison_url: str | Unset = UNSET
    """Dashboard link with the exact route, baseline and candidate windows and selected route call path."""
    application_frames: list[ProfileCallPathFrame] | Unset = UNSET
    investigation_path: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        baseline_deployment_id = str(self.baseline_deployment_id)

        policy_revision = self.policy_revision

        incident_id = str(self.incident_id)

        status: str = self.status

        source: str = self.source

        checked_at = self.checked_at.isoformat()

        baseline = self.baseline.to_dict()

        candidate = self.candidate.to_dict()

        route_check = self.route_check.to_dict()

        evidence_path = self.evidence_path

        comparison_url = self.comparison_url

        application_frames: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.application_frames, Unset):
            application_frames = []
            for application_frames_item_data in self.application_frames:
                application_frames_item = application_frames_item_data.to_dict()
                application_frames.append(application_frames_item)

        investigation_path = self.investigation_path

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "baseline_deployment_id": baseline_deployment_id,
                "policy_revision": policy_revision,
                "incident_id": incident_id,
                "status": status,
                "source": source,
                "checked_at": checked_at,
                "baseline": baseline,
                "candidate": candidate,
                "route_check": route_check,
                "evidence_path": evidence_path,
            }
        )
        if comparison_url is not UNSET:
            field_dict["comparison_url"] = comparison_url
        if application_frames is not UNSET:
            field_dict["application_frames"] = application_frames
        if investigation_path is not UNSET:
            field_dict["investigation_path"] = investigation_path

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_call_path_frame import ProfileCallPathFrame
        from ..models.profile_query import ProfileQuery
        from ..models.profile_route_regression import ProfileRouteRegression

        d = dict(src_dict)
        version = check_profile_route_alert_payload_version(d.pop("version"))

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        baseline_deployment_id = UUID(d.pop("baseline_deployment_id"))

        policy_revision = d.pop("policy_revision")

        incident_id = UUID(d.pop("incident_id"))

        status = check_profile_route_alert_payload_status(d.pop("status"))

        source = check_profile_route_alert_payload_source(d.pop("source"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        baseline = ProfileQuery.from_dict(d.pop("baseline"))

        candidate = ProfileQuery.from_dict(d.pop("candidate"))

        route_check = ProfileRouteRegression.from_dict(d.pop("route_check"))

        evidence_path = d.pop("evidence_path")

        comparison_url = d.pop("comparison_url", UNSET)

        _application_frames = d.pop("application_frames", UNSET)
        application_frames: list[ProfileCallPathFrame] | Unset = UNSET
        if _application_frames is not UNSET:
            application_frames = []
            for application_frames_item_data in _application_frames:
                application_frames_item = ProfileCallPathFrame.from_dict(application_frames_item_data)

                application_frames.append(application_frames_item)

        investigation_path = d.pop("investigation_path", UNSET)

        profile_route_alert_payload = cls(
            version=version,
            app_id=app_id,
            deployment_id=deployment_id,
            baseline_deployment_id=baseline_deployment_id,
            policy_revision=policy_revision,
            incident_id=incident_id,
            status=status,
            source=source,
            checked_at=checked_at,
            baseline=baseline,
            candidate=candidate,
            route_check=route_check,
            evidence_path=evidence_path,
            comparison_url=comparison_url,
            application_frames=application_frames,
            investigation_path=investigation_path,
        )

        profile_route_alert_payload.additional_properties = d
        return profile_route_alert_payload

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

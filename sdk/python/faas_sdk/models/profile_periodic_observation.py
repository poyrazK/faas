from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_periodic_observation_status import (
    ProfilePeriodicObservationStatus,
    check_profile_periodic_observation_status,
)
from ..models.profile_periodic_observation_transition import (
    ProfilePeriodicObservationTransition,
    check_profile_periodic_observation_transition,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_query import ProfileQuery
    from ..models.profile_route_regression import ProfileRouteRegression


T = TypeVar("T", bound="ProfilePeriodicObservation")


@_attrs_define
class ProfilePeriodicObservation:
    id: UUID
    status: ProfilePeriodicObservationStatus
    reason: str
    checked_at: datetime.datetime
    baseline: ProfileQuery
    """Authorized deployment CPU capture window."""
    candidate: ProfileQuery
    """Authorized deployment CPU capture window."""
    route_check: ProfileRouteRegression | Unset = UNSET
    """Retained advisory route-associated sampled CPU/request observation. Missing attribution, sparse requests or
    inadequate capture coverage produces insufficient_data. No rollout decision is changed."""
    incident_id: UUID | Unset = UNSET
    transition: ProfilePeriodicObservationTransition | Unset = UNSET
    investigation_id: UUID | Unset = UNSET
    comparison_url: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        status: str = self.status

        reason = self.reason

        checked_at = self.checked_at.isoformat()

        baseline = self.baseline.to_dict()

        candidate = self.candidate.to_dict()

        route_check: dict[str, Any] | Unset = UNSET
        if not isinstance(self.route_check, Unset):
            route_check = self.route_check.to_dict()

        incident_id: str | Unset = UNSET
        if not isinstance(self.incident_id, Unset):
            incident_id = str(self.incident_id)

        transition: str | Unset = UNSET
        if not isinstance(self.transition, Unset):
            transition = self.transition

        investigation_id: str | Unset = UNSET
        if not isinstance(self.investigation_id, Unset):
            investigation_id = str(self.investigation_id)

        comparison_url = self.comparison_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "status": status,
                "reason": reason,
                "checked_at": checked_at,
                "baseline": baseline,
                "candidate": candidate,
            }
        )
        if route_check is not UNSET:
            field_dict["route_check"] = route_check
        if incident_id is not UNSET:
            field_dict["incident_id"] = incident_id
        if transition is not UNSET:
            field_dict["transition"] = transition
        if investigation_id is not UNSET:
            field_dict["investigation_id"] = investigation_id
        if comparison_url is not UNSET:
            field_dict["comparison_url"] = comparison_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_query import ProfileQuery
        from ..models.profile_route_regression import ProfileRouteRegression

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        status = check_profile_periodic_observation_status(d.pop("status"))

        reason = d.pop("reason")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        baseline = ProfileQuery.from_dict(d.pop("baseline"))

        candidate = ProfileQuery.from_dict(d.pop("candidate"))

        _route_check = d.pop("route_check", UNSET)
        route_check: ProfileRouteRegression | Unset
        if isinstance(_route_check, Unset):
            route_check = UNSET
        else:
            route_check = ProfileRouteRegression.from_dict(_route_check)

        _incident_id = d.pop("incident_id", UNSET)
        incident_id: UUID | Unset
        if isinstance(_incident_id, Unset):
            incident_id = UNSET
        else:
            incident_id = UUID(_incident_id)

        _transition = d.pop("transition", UNSET)
        transition: ProfilePeriodicObservationTransition | Unset
        if isinstance(_transition, Unset):
            transition = UNSET
        else:
            transition = check_profile_periodic_observation_transition(_transition)

        _investigation_id = d.pop("investigation_id", UNSET)
        investigation_id: UUID | Unset
        if isinstance(_investigation_id, Unset):
            investigation_id = UNSET
        else:
            investigation_id = UUID(_investigation_id)

        comparison_url = d.pop("comparison_url", UNSET)

        profile_periodic_observation = cls(
            id=id,
            status=status,
            reason=reason,
            checked_at=checked_at,
            baseline=baseline,
            candidate=candidate,
            route_check=route_check,
            incident_id=incident_id,
            transition=transition,
            investigation_id=investigation_id,
            comparison_url=comparison_url,
        )

        profile_periodic_observation.additional_properties = d
        return profile_periodic_observation

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

from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_deployment_policy_config import ProfileDeploymentPolicyConfig
    from ..models.profile_periodic_observation import ProfilePeriodicObservation
    from ..models.profile_query import ProfileQuery


T = TypeVar("T", bound="ProfilePeriodicMonitor")


@_attrs_define
class ProfilePeriodicMonitor:
    """Bounded periodic route evidence and consecutive observations for one deployment and policy revision."""

    active: bool
    """Whether this monitor still matches the running deployment, current policy and eligible account."""
    id: UUID
    app_id: UUID
    deployment_id: UUID
    scope: str
    route: str
    policy_revision: int
    config: ProfileDeploymentPolicyConfig
    """Opt-in background comparison policy. Collection must already be enabled on applications. Runtime filters
    apply to both deployments; capture coverage is not instrumentation completeness."""
    candidate: ProfileQuery
    """Authorized deployment CPU capture window."""
    attempts: int
    next_attempt_at: datetime.datetime
    history: list[ProfilePeriodicObservation]
    baseline: ProfileQuery | Unset = UNSET
    """Authorized deployment CPU capture window."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        active = self.active

        id = str(self.id)

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        scope = self.scope

        route = self.route

        policy_revision = self.policy_revision

        config = self.config.to_dict()

        candidate = self.candidate.to_dict()

        attempts = self.attempts

        next_attempt_at = self.next_attempt_at.isoformat()

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        baseline: dict[str, Any] | Unset = UNSET
        if not isinstance(self.baseline, Unset):
            baseline = self.baseline.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "active": active,
                "id": id,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "scope": scope,
                "route": route,
                "policy_revision": policy_revision,
                "config": config,
                "candidate": candidate,
                "attempts": attempts,
                "next_attempt_at": next_attempt_at,
                "history": history,
            }
        )
        if baseline is not UNSET:
            field_dict["baseline"] = baseline

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_deployment_policy_config import ProfileDeploymentPolicyConfig
        from ..models.profile_periodic_observation import ProfilePeriodicObservation
        from ..models.profile_query import ProfileQuery

        d = dict(src_dict)
        active = d.pop("active")

        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        scope = d.pop("scope")

        route = d.pop("route")

        policy_revision = d.pop("policy_revision")

        config = ProfileDeploymentPolicyConfig.from_dict(d.pop("config"))

        candidate = ProfileQuery.from_dict(d.pop("candidate"))

        attempts = d.pop("attempts")

        next_attempt_at = datetime.datetime.fromisoformat(d.pop("next_attempt_at"))

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = ProfilePeriodicObservation.from_dict(history_item_data)

            history.append(history_item)

        _baseline = d.pop("baseline", UNSET)
        baseline: ProfileQuery | Unset
        if isinstance(_baseline, Unset):
            baseline = UNSET
        else:
            baseline = ProfileQuery.from_dict(_baseline)

        profile_periodic_monitor = cls(
            active=active,
            id=id,
            app_id=app_id,
            deployment_id=deployment_id,
            scope=scope,
            route=route,
            policy_revision=policy_revision,
            config=config,
            candidate=candidate,
            attempts=attempts,
            next_attempt_at=next_attempt_at,
            history=history,
            baseline=baseline,
        )

        profile_periodic_monitor.additional_properties = d
        return profile_periodic_monitor

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

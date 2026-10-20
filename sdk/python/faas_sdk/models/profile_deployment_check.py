from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_deployment_check_status import ProfileDeploymentCheckStatus, check_profile_deployment_check_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_deployment_policy_config import ProfileDeploymentPolicyConfig
    from ..models.profile_query import ProfileQuery


T = TypeVar("T", bound="ProfileDeploymentCheck")


@_attrs_define
class ProfileDeploymentCheck:
    """Durable automatic CPU-check receipt. Only bounded metadata is retained; raw samples remain under backend retention.
    Retry and lease recovery keep the original deployment pair and windows. Missing predecessors yield an inconclusive
    receipt without an investigation. If the saved-investigation quota is full, the result remains recorded with its
    original comparison link and an explicit explanation.

    """

    deployment_id: UUID
    app_id: UUID
    scope: str
    policy_revision: int
    config: ProfileDeploymentPolicyConfig
    """Opt-in background comparison policy. Collection must already be enabled on applications. Runtime filters
    apply to both deployments; capture coverage is not instrumentation completeness."""
    candidate: ProfileQuery
    """Authorized deployment CPU capture window."""
    status: ProfileDeploymentCheckStatus
    reason: str
    attempts: int
    created_at: datetime.datetime
    baseline: ProfileQuery | Unset = UNSET
    """Authorized deployment CPU capture window."""
    next_attempt_at: datetime.datetime | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    investigation_id: UUID | Unset = UNSET
    comparison_url: str | Unset = UNSET
    """Relative authenticated dashboard link to saved metadata or the original exact comparison windows."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        app_id = str(self.app_id)

        scope = self.scope

        policy_revision = self.policy_revision

        config = self.config.to_dict()

        candidate = self.candidate.to_dict()

        status: str = self.status

        reason = self.reason

        attempts = self.attempts

        created_at = self.created_at.isoformat()

        baseline: dict[str, Any] | Unset = UNSET
        if not isinstance(self.baseline, Unset):
            baseline = self.baseline.to_dict()

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        investigation_id: str | Unset = UNSET
        if not isinstance(self.investigation_id, Unset):
            investigation_id = str(self.investigation_id)

        comparison_url = self.comparison_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "app_id": app_id,
                "scope": scope,
                "policy_revision": policy_revision,
                "config": config,
                "candidate": candidate,
                "status": status,
                "reason": reason,
                "attempts": attempts,
                "created_at": created_at,
            }
        )
        if baseline is not UNSET:
            field_dict["baseline"] = baseline
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if investigation_id is not UNSET:
            field_dict["investigation_id"] = investigation_id
        if comparison_url is not UNSET:
            field_dict["comparison_url"] = comparison_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_deployment_policy_config import ProfileDeploymentPolicyConfig
        from ..models.profile_query import ProfileQuery

        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        policy_revision = d.pop("policy_revision")

        config = ProfileDeploymentPolicyConfig.from_dict(d.pop("config"))

        candidate = ProfileQuery.from_dict(d.pop("candidate"))

        status = check_profile_deployment_check_status(d.pop("status"))

        reason = d.pop("reason")

        attempts = d.pop("attempts")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _baseline = d.pop("baseline", UNSET)
        baseline: ProfileQuery | Unset
        if isinstance(_baseline, Unset):
            baseline = UNSET
        else:
            baseline = ProfileQuery.from_dict(_baseline)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        _investigation_id = d.pop("investigation_id", UNSET)
        investigation_id: UUID | Unset
        if isinstance(_investigation_id, Unset):
            investigation_id = UNSET
        else:
            investigation_id = UUID(_investigation_id)

        comparison_url = d.pop("comparison_url", UNSET)

        profile_deployment_check = cls(
            deployment_id=deployment_id,
            app_id=app_id,
            scope=scope,
            policy_revision=policy_revision,
            config=config,
            candidate=candidate,
            status=status,
            reason=reason,
            attempts=attempts,
            created_at=created_at,
            baseline=baseline,
            next_attempt_at=next_attempt_at,
            completed_at=completed_at,
            investigation_id=investigation_id,
            comparison_url=comparison_url,
        )

        profile_deployment_check.additional_properties = d
        return profile_deployment_check

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

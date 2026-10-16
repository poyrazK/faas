from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_environment_cache_purge_request import CreateEnvironmentCachePurgeRequest
    from ..models.set_environment_cache_policy_request import SetEnvironmentCachePolicyRequest


T = TypeVar("T", bound="CreateEnvironmentCacheScheduleRequest")


@_attrs_define
class CreateEnvironmentCacheScheduleRequest:
    """One durable environment cache action; exactly one policy or purge is required, with request_id equal to schedule_id."""

    schedule_id: UUID
    app_id: UUID
    project_id: UUID
    environment_id: UUID
    execute_at: datetime.datetime
    policy: SetEnvironmentCachePolicyRequest | Unset = UNSET
    """Complete named-environment replacement with immutable identity, revision guard and transactional retry ID."""
    purge: CreateEnvironmentCachePurgeRequest | Unset = UNSET
    """Immutable named-environment purge target and stable retry identity; path must be explicit even for whole-
    environment purging."""

    def to_dict(self) -> dict[str, Any]:
        schedule_id = str(self.schedule_id)

        app_id = str(self.app_id)

        project_id = str(self.project_id)

        environment_id = str(self.environment_id)

        execute_at = self.execute_at.isoformat()

        policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.policy, Unset):
            policy = self.policy.to_dict()

        purge: dict[str, Any] | Unset = UNSET
        if not isinstance(self.purge, Unset):
            purge = self.purge.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "schedule_id": schedule_id,
                "app_id": app_id,
                "project_id": project_id,
                "environment_id": environment_id,
                "execute_at": execute_at,
            }
        )
        if policy is not UNSET:
            field_dict["policy"] = policy
        if purge is not UNSET:
            field_dict["purge"] = purge

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_environment_cache_purge_request import CreateEnvironmentCachePurgeRequest
        from ..models.set_environment_cache_policy_request import SetEnvironmentCachePolicyRequest

        d = dict(src_dict)
        schedule_id = UUID(d.pop("schedule_id"))

        app_id = UUID(d.pop("app_id"))

        project_id = UUID(d.pop("project_id"))

        environment_id = UUID(d.pop("environment_id"))

        execute_at = datetime.datetime.fromisoformat(d.pop("execute_at"))

        _policy = d.pop("policy", UNSET)
        policy: SetEnvironmentCachePolicyRequest | Unset
        if isinstance(_policy, Unset):
            policy = UNSET
        else:
            policy = SetEnvironmentCachePolicyRequest.from_dict(_policy)

        _purge = d.pop("purge", UNSET)
        purge: CreateEnvironmentCachePurgeRequest | Unset
        if isinstance(_purge, Unset):
            purge = UNSET
        else:
            purge = CreateEnvironmentCachePurgeRequest.from_dict(_purge)

        create_environment_cache_schedule_request = cls(
            schedule_id=schedule_id,
            app_id=app_id,
            project_id=project_id,
            environment_id=environment_id,
            execute_at=execute_at,
            policy=policy,
            purge=purge,
        )

        return create_environment_cache_schedule_request

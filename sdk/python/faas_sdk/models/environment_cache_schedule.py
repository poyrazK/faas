from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.environment_cache_schedule_status import (
    EnvironmentCacheScheduleStatus,
    check_environment_cache_schedule_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_environment_cache_purge_request import CreateEnvironmentCachePurgeRequest
    from ..models.environment_cache_policy import EnvironmentCachePolicy
    from ..models.environment_cache_purge_receipt import EnvironmentCachePurgeReceipt
    from ..models.set_environment_cache_policy_request import SetEnvironmentCachePolicyRequest


T = TypeVar("T", bound="EnvironmentCacheSchedule")


@_attrs_define
class EnvironmentCacheSchedule:
    """Accepted cache delegation with execution state and optional exact action receipt, distinct from gateway convergence."""

    schedule_id: UUID
    app_id: UUID
    project_id: UUID
    environment_id: UUID
    execute_at: datetime.datetime
    scope: str
    status: EnvironmentCacheScheduleStatus
    created_at: datetime.datetime
    policy: SetEnvironmentCachePolicyRequest | Unset = UNSET
    """Complete named-environment replacement with immutable identity, revision guard and transactional retry ID."""
    purge: CreateEnvironmentCachePurgeRequest | Unset = UNSET
    """Immutable named-environment purge target and stable retry identity; path must be explicit even for whole-
    environment purging."""
    finished_at: datetime.datetime | Unset = UNSET
    error_code: str | Unset = UNSET
    policy_result: EnvironmentCachePolicy | Unset = UNSET
    """Desired cache mode and monotonic revision for one named environment; absence preserves app inheritance at
    revision zero."""
    purge_result: EnvironmentCachePurgeReceipt | Unset = UNSET
    """Stable durable logical cache-purge intent for one immutable environment host."""
    replaces_schedule_id: UUID | Unset = UNSET
    """Original schedule cancelled atomically when this successor was accepted."""
    replaced_by_schedule_id: UUID | Unset = UNSET
    """Successor UUID replacing this cancelled original; cancellation of this original does not withdraw that
    successor."""

    def to_dict(self) -> dict[str, Any]:
        schedule_id = str(self.schedule_id)

        app_id = str(self.app_id)

        project_id = str(self.project_id)

        environment_id = str(self.environment_id)

        execute_at = self.execute_at.isoformat()

        scope = self.scope

        status: str = self.status

        created_at = self.created_at.isoformat()

        policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.policy, Unset):
            policy = self.policy.to_dict()

        purge: dict[str, Any] | Unset = UNSET
        if not isinstance(self.purge, Unset):
            purge = self.purge.to_dict()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        error_code = self.error_code

        policy_result: dict[str, Any] | Unset = UNSET
        if not isinstance(self.policy_result, Unset):
            policy_result = self.policy_result.to_dict()

        purge_result: dict[str, Any] | Unset = UNSET
        if not isinstance(self.purge_result, Unset):
            purge_result = self.purge_result.to_dict()

        replaces_schedule_id: str | Unset = UNSET
        if not isinstance(self.replaces_schedule_id, Unset):
            replaces_schedule_id = str(self.replaces_schedule_id)

        replaced_by_schedule_id: str | Unset = UNSET
        if not isinstance(self.replaced_by_schedule_id, Unset):
            replaced_by_schedule_id = str(self.replaced_by_schedule_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "schedule_id": schedule_id,
                "app_id": app_id,
                "project_id": project_id,
                "environment_id": environment_id,
                "execute_at": execute_at,
                "scope": scope,
                "status": status,
                "created_at": created_at,
            }
        )
        if policy is not UNSET:
            field_dict["policy"] = policy
        if purge is not UNSET:
            field_dict["purge"] = purge
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if policy_result is not UNSET:
            field_dict["policy_result"] = policy_result
        if purge_result is not UNSET:
            field_dict["purge_result"] = purge_result
        if replaces_schedule_id is not UNSET:
            field_dict["replaces_schedule_id"] = replaces_schedule_id
        if replaced_by_schedule_id is not UNSET:
            field_dict["replaced_by_schedule_id"] = replaced_by_schedule_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_environment_cache_purge_request import CreateEnvironmentCachePurgeRequest
        from ..models.environment_cache_policy import EnvironmentCachePolicy
        from ..models.environment_cache_purge_receipt import EnvironmentCachePurgeReceipt
        from ..models.set_environment_cache_policy_request import SetEnvironmentCachePolicyRequest

        d = dict(src_dict)
        schedule_id = UUID(d.pop("schedule_id"))

        app_id = UUID(d.pop("app_id"))

        project_id = UUID(d.pop("project_id"))

        environment_id = UUID(d.pop("environment_id"))

        execute_at = datetime.datetime.fromisoformat(d.pop("execute_at"))

        scope = d.pop("scope")

        status = check_environment_cache_schedule_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

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

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        error_code = d.pop("error_code", UNSET)

        _policy_result = d.pop("policy_result", UNSET)
        policy_result: EnvironmentCachePolicy | Unset
        if isinstance(_policy_result, Unset):
            policy_result = UNSET
        else:
            policy_result = EnvironmentCachePolicy.from_dict(_policy_result)

        _purge_result = d.pop("purge_result", UNSET)
        purge_result: EnvironmentCachePurgeReceipt | Unset
        if isinstance(_purge_result, Unset):
            purge_result = UNSET
        else:
            purge_result = EnvironmentCachePurgeReceipt.from_dict(_purge_result)

        _replaces_schedule_id = d.pop("replaces_schedule_id", UNSET)
        replaces_schedule_id: UUID | Unset
        if isinstance(_replaces_schedule_id, Unset):
            replaces_schedule_id = UNSET
        else:
            replaces_schedule_id = UUID(_replaces_schedule_id)

        _replaced_by_schedule_id = d.pop("replaced_by_schedule_id", UNSET)
        replaced_by_schedule_id: UUID | Unset
        if isinstance(_replaced_by_schedule_id, Unset):
            replaced_by_schedule_id = UNSET
        else:
            replaced_by_schedule_id = UUID(_replaced_by_schedule_id)

        environment_cache_schedule = cls(
            schedule_id=schedule_id,
            app_id=app_id,
            project_id=project_id,
            environment_id=environment_id,
            execute_at=execute_at,
            scope=scope,
            status=status,
            created_at=created_at,
            policy=policy,
            purge=purge,
            finished_at=finished_at,
            error_code=error_code,
            policy_result=policy_result,
            purge_result=purge_result,
            replaces_schedule_id=replaces_schedule_id,
            replaced_by_schedule_id=replaced_by_schedule_id,
        )

        return environment_cache_schedule

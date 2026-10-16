from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.environment_cache_schedule_event_action import (
    EnvironmentCacheScheduleEventAction,
    check_environment_cache_schedule_event_action,
)
from ..models.environment_cache_schedule_event_kind import (
    EnvironmentCacheScheduleEventKind,
    check_environment_cache_schedule_event_kind,
)
from ..models.environment_cache_schedule_event_status import (
    EnvironmentCacheScheduleEventStatus,
    check_environment_cache_schedule_event_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EnvironmentCacheScheduleEvent")


@_attrs_define
class EnvironmentCacheScheduleEvent:
    """Immutable schedule transition metadata, with receipt identity only for confirmed execution and no gateway-
    convergence claim.

    """

    sequence: int
    schedule_id: UUID
    kind: EnvironmentCacheScheduleEventKind
    status: EnvironmentCacheScheduleEventStatus
    action: EnvironmentCacheScheduleEventAction
    execute_at: datetime.datetime
    occurred_at: datetime.datetime
    related_schedule_id: UUID | Unset = UNSET
    """Successor for rescheduled originals or predecessor for accepted successors; absent otherwise."""
    error_code: str | Unset = UNSET
    policy_revision: int | Unset = UNSET
    purge_generation: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        schedule_id = str(self.schedule_id)

        kind: str = self.kind

        status: str = self.status

        action: str = self.action

        execute_at = self.execute_at.isoformat()

        occurred_at = self.occurred_at.isoformat()

        related_schedule_id: str | Unset = UNSET
        if not isinstance(self.related_schedule_id, Unset):
            related_schedule_id = str(self.related_schedule_id)

        error_code = self.error_code

        policy_revision = self.policy_revision

        purge_generation = self.purge_generation

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "sequence": sequence,
                "schedule_id": schedule_id,
                "kind": kind,
                "status": status,
                "action": action,
                "execute_at": execute_at,
                "occurred_at": occurred_at,
            }
        )
        if related_schedule_id is not UNSET:
            field_dict["related_schedule_id"] = related_schedule_id
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if policy_revision is not UNSET:
            field_dict["policy_revision"] = policy_revision
        if purge_generation is not UNSET:
            field_dict["purge_generation"] = purge_generation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sequence = d.pop("sequence")

        schedule_id = UUID(d.pop("schedule_id"))

        kind = check_environment_cache_schedule_event_kind(d.pop("kind"))

        status = check_environment_cache_schedule_event_status(d.pop("status"))

        action = check_environment_cache_schedule_event_action(d.pop("action"))

        execute_at = datetime.datetime.fromisoformat(d.pop("execute_at"))

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        _related_schedule_id = d.pop("related_schedule_id", UNSET)
        related_schedule_id: UUID | Unset
        if isinstance(_related_schedule_id, Unset):
            related_schedule_id = UNSET
        else:
            related_schedule_id = UUID(_related_schedule_id)

        error_code = d.pop("error_code", UNSET)

        policy_revision = d.pop("policy_revision", UNSET)

        purge_generation = d.pop("purge_generation", UNSET)

        environment_cache_schedule_event = cls(
            sequence=sequence,
            schedule_id=schedule_id,
            kind=kind,
            status=status,
            action=action,
            execute_at=execute_at,
            occurred_at=occurred_at,
            related_schedule_id=related_schedule_id,
            error_code=error_code,
            policy_revision=policy_revision,
            purge_generation=purge_generation,
        )

        return environment_cache_schedule_event

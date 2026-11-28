from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_execution_lineage_retry_job_control_filters_action import (
    EventExecutionLineageRetryJobControlFiltersAction,
    check_event_execution_lineage_retry_job_control_filters_action,
)
from ..models.event_execution_lineage_retry_job_control_filters_credential_type import (
    EventExecutionLineageRetryJobControlFiltersCredentialType,
    check_event_execution_lineage_retry_job_control_filters_credential_type,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventExecutionLineageRetryJobControlFilters")


@_attrs_define
class EventExecutionLineageRetryJobControlFilters:
    """Exact action and original actor filters applied before control-receipt pagination."""

    action: EventExecutionLineageRetryJobControlFiltersAction | Unset = UNSET
    actor_id: str | Unset = UNSET
    """Exact actor UUID in hyphenated form or store for an internal caller. Empty means no filter."""
    credential_type: EventExecutionLineageRetryJobControlFiltersCredentialType | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        actor_id = self.actor_id

        credential_type: str | Unset = UNSET
        if not isinstance(self.credential_type, Unset):
            credential_type = self.credential_type

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if action is not UNSET:
            field_dict["action"] = action
        if actor_id is not UNSET:
            field_dict["actor_id"] = actor_id
        if credential_type is not UNSET:
            field_dict["credential_type"] = credential_type

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _action = d.pop("action", UNSET)
        action: EventExecutionLineageRetryJobControlFiltersAction | Unset
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = check_event_execution_lineage_retry_job_control_filters_action(_action)

        actor_id = d.pop("actor_id", UNSET)

        _credential_type = d.pop("credential_type", UNSET)
        credential_type: EventExecutionLineageRetryJobControlFiltersCredentialType | Unset
        if isinstance(_credential_type, Unset):
            credential_type = UNSET
        else:
            credential_type = check_event_execution_lineage_retry_job_control_filters_credential_type(_credential_type)

        event_execution_lineage_retry_job_control_filters = cls(
            action=action,
            actor_id=actor_id,
            credential_type=credential_type,
        )

        return event_execution_lineage_retry_job_control_filters

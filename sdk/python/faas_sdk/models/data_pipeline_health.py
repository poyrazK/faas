from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_pipeline_health_action import DataPipelineHealthAction, check_data_pipeline_health_action
from ..models.data_pipeline_health_state import DataPipelineHealthState, check_data_pipeline_health_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="DataPipelineHealth")


@_attrs_define
class DataPipelineHealth:
    """Read-time freshness assessment and safe recovery guidance. Unconfirmed checkpoints can occur on idle sources."""

    state: DataPipelineHealthState
    code: str
    message: str
    recovery: str
    requires_confirmation: bool
    action: DataPipelineHealthAction | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        code = self.code

        message = self.message

        recovery = self.recovery

        requires_confirmation = self.requires_confirmation

        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "state": state,
                "code": code,
                "message": message,
                "recovery": recovery,
                "requires_confirmation": requires_confirmation,
            }
        )
        if action is not UNSET:
            field_dict["action"] = action

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_data_pipeline_health_state(d.pop("state"))

        code = d.pop("code")

        message = d.pop("message")

        recovery = d.pop("recovery")

        requires_confirmation = d.pop("requires_confirmation")

        _action = d.pop("action", UNSET)
        action: DataPipelineHealthAction | Unset
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = check_data_pipeline_health_action(_action)

        data_pipeline_health = cls(
            state=state,
            code=code,
            message=message,
            recovery=recovery,
            requires_confirmation=requires_confirmation,
            action=action,
        )

        return data_pipeline_health

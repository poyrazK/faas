from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_recovery_response_action import WorkflowRecoveryResponseAction


T = TypeVar("T", bound="WorkflowRecoveryResponsePlan")


@_attrs_define
class WorkflowRecoveryResponsePlan:
    """Saved reviewed actions pinned to an app and parent recovery job. Explicit apply uses stable per-action identifiers.
    Actions commit independently.

    """

    version: int
    app: str
    app_id: UUID
    job_id: UUID
    key: str
    created_at: datetime.datetime
    actions: list[WorkflowRecoveryResponseAction]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        app = self.app

        app_id = str(self.app_id)

        job_id = str(self.job_id)

        key = self.key

        created_at = self.created_at.isoformat()

        actions = []
        for actions_item_data in self.actions:
            actions_item = actions_item_data.to_dict()
            actions.append(actions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "app": app,
                "app_id": app_id,
                "job_id": job_id,
                "key": key,
                "created_at": created_at,
                "actions": actions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_response_action import WorkflowRecoveryResponseAction

        d = dict(src_dict)
        version = d.pop("version")

        app = d.pop("app")

        app_id = UUID(d.pop("app_id"))

        job_id = UUID(d.pop("job_id"))

        key = d.pop("key")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        actions = []
        _actions = d.pop("actions")
        for actions_item_data in _actions:
            actions_item = WorkflowRecoveryResponseAction.from_dict(actions_item_data)

            actions.append(actions_item)

        workflow_recovery_response_plan = cls(
            version=version,
            app=app,
            app_id=app_id,
            job_id=job_id,
            key=key,
            created_at=created_at,
            actions=actions,
        )

        workflow_recovery_response_plan.additional_properties = d
        return workflow_recovery_response_plan

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

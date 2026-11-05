from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.activity_actor_response import ActivityActorResponse
    from ..models.activity_resource_response import ActivityResourceResponse
    from ..models.org_activity_response_data import OrgActivityResponseData


T = TypeVar("T", bound="OrgActivityResponse")


@_attrs_define
class OrgActivityResponse:
    """One safe, display-ready organization activity fact."""

    id: str
    """Monotonic bigint row id encoded as a string."""
    occurred_at: datetime.datetime
    kind: str
    summary: str
    actor: ActivityActorResponse
    """Captured identity responsible for an activity item."""
    resource: ActivityResourceResponse
    """Primary infrastructure object affected by an activity item."""
    data: OrgActivityResponseData
    """Kind-specific non-secret display metadata."""
    app_id: UUID | Unset = UNSET
    project_id: UUID | Unset = UNSET
    deployment_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        occurred_at = self.occurred_at.isoformat()

        kind = self.kind

        summary = self.summary

        actor = self.actor.to_dict()

        resource = self.resource.to_dict()

        data = self.data.to_dict()

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        project_id: str | Unset = UNSET
        if not isinstance(self.project_id, Unset):
            project_id = str(self.project_id)

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "occurred_at": occurred_at,
                "kind": kind,
                "summary": summary,
                "actor": actor,
                "resource": resource,
                "data": data,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.activity_actor_response import ActivityActorResponse
        from ..models.activity_resource_response import ActivityResourceResponse
        from ..models.org_activity_response_data import OrgActivityResponseData

        d = dict(src_dict)
        id = d.pop("id")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        kind = d.pop("kind")

        summary = d.pop("summary")

        actor = ActivityActorResponse.from_dict(d.pop("actor"))

        resource = ActivityResourceResponse.from_dict(d.pop("resource"))

        data = OrgActivityResponseData.from_dict(d.pop("data"))

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        _project_id = d.pop("project_id", UNSET)
        project_id: UUID | Unset
        if isinstance(_project_id, Unset):
            project_id = UNSET
        else:
            project_id = UUID(_project_id)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        org_activity_response = cls(
            id=id,
            occurred_at=occurred_at,
            kind=kind,
            summary=summary,
            actor=actor,
            resource=resource,
            data=data,
            app_id=app_id,
            project_id=project_id,
            deployment_id=deployment_id,
        )

        org_activity_response.additional_properties = d
        return org_activity_response

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

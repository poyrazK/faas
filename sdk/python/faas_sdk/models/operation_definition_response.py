from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_definition_spec import OperationDefinitionSpec


T = TypeVar("T", bound="OperationDefinitionResponse")


@_attrs_define
class OperationDefinitionResponse:
    """Definition identity, content revision and deployment pins."""

    id: UUID
    app_id: UUID
    scope: str
    revision: str
    deployment_id: UUID
    spec: OperationDefinitionSpec
    """Resolved immutable contract for one HTTP handler. Ownership comes from verified authentication, never input
    fields. Production admission stays disabled until the HTTP execution adapter is qualified."""
    created_at: datetime.datetime
    release_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        scope = self.scope

        revision = self.revision

        deployment_id = str(self.deployment_id)

        spec = self.spec.to_dict()

        created_at = self.created_at.isoformat()

        release_id: str | Unset = UNSET
        if not isinstance(self.release_id, Unset):
            release_id = str(self.release_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "scope": scope,
                "revision": revision,
                "deployment_id": deployment_id,
                "spec": spec,
                "created_at": created_at,
            }
        )
        if release_id is not UNSET:
            field_dict["release_id"] = release_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_definition_spec import OperationDefinitionSpec

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        revision = d.pop("revision")

        deployment_id = UUID(d.pop("deployment_id"))

        spec = OperationDefinitionSpec.from_dict(d.pop("spec"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _release_id = d.pop("release_id", UNSET)
        release_id: UUID | Unset
        if isinstance(_release_id, Unset):
            release_id = UNSET
        else:
            release_id = UUID(_release_id)

        operation_definition_response = cls(
            id=id,
            app_id=app_id,
            scope=scope,
            revision=revision,
            deployment_id=deployment_id,
            spec=spec,
            created_at=created_at,
            release_id=release_id,
        )

        operation_definition_response.additional_properties = d
        return operation_definition_response

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

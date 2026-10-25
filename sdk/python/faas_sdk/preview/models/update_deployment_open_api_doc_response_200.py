from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.update_deployment_open_api_doc_response_200_source import (
    UpdateDeploymentOpenAPIDocResponse200Source,
    check_update_deployment_open_api_doc_response_200_source,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.update_deployment_open_api_doc_response_200_doc import UpdateDeploymentOpenAPIDocResponse200Doc


T = TypeVar("T", bound="UpdateDeploymentOpenAPIDocResponse200")


@_attrs_define
class UpdateDeploymentOpenAPIDocResponse200:
    """The stored OpenAPI doc with its metadata envelope."""

    deployment_id: UUID
    account_id: UUID
    app_id: UUID
    source: UpdateDeploymentOpenAPIDocResponse200Source
    byte_size: int
    captured_at: datetime.datetime
    updated_at: datetime.datetime
    doc_sha256: str | Unset = UNSET
    """Lower-case hex SHA-256 of the stored body."""
    truncated: bool | Unset = UNSET
    doc: UpdateDeploymentOpenAPIDocResponse200Doc | Unset = UNSET
    """The OpenAPI document body."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        account_id = str(self.account_id)

        app_id = str(self.app_id)

        source: str = self.source

        byte_size = self.byte_size

        captured_at = self.captured_at.isoformat()

        updated_at = self.updated_at.isoformat()

        doc_sha256 = self.doc_sha256

        truncated = self.truncated

        doc: dict[str, Any] | Unset = UNSET
        if not isinstance(self.doc, Unset):
            doc = self.doc.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "account_id": account_id,
                "app_id": app_id,
                "source": source,
                "byte_size": byte_size,
                "captured_at": captured_at,
                "updated_at": updated_at,
            }
        )
        if doc_sha256 is not UNSET:
            field_dict["doc_sha256"] = doc_sha256
        if truncated is not UNSET:
            field_dict["truncated"] = truncated
        if doc is not UNSET:
            field_dict["doc"] = doc

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.update_deployment_open_api_doc_response_200_doc import UpdateDeploymentOpenAPIDocResponse200Doc

        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        account_id = UUID(d.pop("account_id"))

        app_id = UUID(d.pop("app_id"))

        source = check_update_deployment_open_api_doc_response_200_source(d.pop("source"))

        byte_size = d.pop("byte_size")

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        doc_sha256 = d.pop("doc_sha256", UNSET)

        truncated = d.pop("truncated", UNSET)

        _doc = d.pop("doc", UNSET)
        doc: UpdateDeploymentOpenAPIDocResponse200Doc | Unset
        if isinstance(_doc, Unset):
            doc = UNSET
        else:
            doc = UpdateDeploymentOpenAPIDocResponse200Doc.from_dict(_doc)

        update_deployment_open_api_doc_response_200 = cls(
            deployment_id=deployment_id,
            account_id=account_id,
            app_id=app_id,
            source=source,
            byte_size=byte_size,
            captured_at=captured_at,
            updated_at=updated_at,
            doc_sha256=doc_sha256,
            truncated=truncated,
            doc=doc,
        )

        update_deployment_open_api_doc_response_200.additional_properties = d
        return update_deployment_open_api_doc_response_200

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

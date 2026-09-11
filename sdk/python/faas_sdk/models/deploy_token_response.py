from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deploy_token_response_scopes_item import (
    DeployTokenResponseScopesItem,
    check_deploy_token_response_scopes_item,
)
from ..models.deploy_token_response_status import DeployTokenResponseStatus, check_deploy_token_response_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="DeployTokenResponse")


@_attrs_define
class DeployTokenResponse:
    """Metadata for a per-app deploy token. The fp_deploy_ plaintext is returned only on create and rotate responses."""

    id: UUID
    app_id: UUID
    prefix: str
    scopes: list[DeployTokenResponseScopesItem]
    status: DeployTokenResponseStatus
    created_at: datetime.datetime
    expires_at: datetime.datetime
    label: str | Unset = UNSET
    last_used_at: datetime.datetime | Unset = UNSET
    revoked_at: datetime.datetime | Unset = UNSET
    rotated_from_id: UUID | Unset = UNSET
    plaintext: str | Unset = UNSET
    """Present only on create/rotate. Store it in CI immediately; it cannot be retrieved later."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        prefix = self.prefix

        scopes = []
        for scopes_item_data in self.scopes:
            scopes_item: str = scopes_item_data
            scopes.append(scopes_item)

        status: str = self.status

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        label = self.label

        last_used_at: str | Unset = UNSET
        if not isinstance(self.last_used_at, Unset):
            last_used_at = self.last_used_at.isoformat()

        revoked_at: str | Unset = UNSET
        if not isinstance(self.revoked_at, Unset):
            revoked_at = self.revoked_at.isoformat()

        rotated_from_id: str | Unset = UNSET
        if not isinstance(self.rotated_from_id, Unset):
            rotated_from_id = str(self.rotated_from_id)

        plaintext = self.plaintext

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "prefix": prefix,
                "scopes": scopes,
                "status": status,
                "created_at": created_at,
                "expires_at": expires_at,
            }
        )
        if label is not UNSET:
            field_dict["label"] = label
        if last_used_at is not UNSET:
            field_dict["last_used_at"] = last_used_at
        if revoked_at is not UNSET:
            field_dict["revoked_at"] = revoked_at
        if rotated_from_id is not UNSET:
            field_dict["rotated_from_id"] = rotated_from_id
        if plaintext is not UNSET:
            field_dict["plaintext"] = plaintext

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        prefix = d.pop("prefix")

        scopes = []
        _scopes = d.pop("scopes")
        for scopes_item_data in _scopes:
            scopes_item = check_deploy_token_response_scopes_item(scopes_item_data)

            scopes.append(scopes_item)

        status = check_deploy_token_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        label = d.pop("label", UNSET)

        _last_used_at = d.pop("last_used_at", UNSET)
        last_used_at: datetime.datetime | Unset
        if isinstance(_last_used_at, Unset):
            last_used_at = UNSET
        else:
            last_used_at = datetime.datetime.fromisoformat(_last_used_at)

        _revoked_at = d.pop("revoked_at", UNSET)
        revoked_at: datetime.datetime | Unset
        if isinstance(_revoked_at, Unset):
            revoked_at = UNSET
        else:
            revoked_at = datetime.datetime.fromisoformat(_revoked_at)

        _rotated_from_id = d.pop("rotated_from_id", UNSET)
        rotated_from_id: UUID | Unset
        if isinstance(_rotated_from_id, Unset):
            rotated_from_id = UNSET
        else:
            rotated_from_id = UUID(_rotated_from_id)

        plaintext = d.pop("plaintext", UNSET)

        deploy_token_response = cls(
            id=id,
            app_id=app_id,
            prefix=prefix,
            scopes=scopes,
            status=status,
            created_at=created_at,
            expires_at=expires_at,
            label=label,
            last_used_at=last_used_at,
            revoked_at=revoked_at,
            rotated_from_id=rotated_from_id,
            plaintext=plaintext,
        )

        deploy_token_response.additional_properties = d
        return deploy_token_response

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

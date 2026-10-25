from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.deploy_token_response import DeployTokenResponse


T = TypeVar("T", bound="RotateDeployTokenResponse")


@_attrs_define
class RotateDeployTokenResponse:
    """Replacement deploy-token metadata and one-time plaintext, plus the revoked predecessor id."""

    token: DeployTokenResponse
    """Metadata for a per-app deploy token. The fp_deploy_ plaintext is returned only on create and rotate
    responses."""
    token_plaintext: str
    old_token_id: UUID
    old_token_expires_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        token = self.token.to_dict()

        token_plaintext = self.token_plaintext

        old_token_id = str(self.old_token_id)

        old_token_expires_at: str | Unset = UNSET
        if not isinstance(self.old_token_expires_at, Unset):
            old_token_expires_at = self.old_token_expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "token": token,
                "token_plaintext": token_plaintext,
                "old_token_id": old_token_id,
            }
        )
        if old_token_expires_at is not UNSET:
            field_dict["old_token_expires_at"] = old_token_expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deploy_token_response import DeployTokenResponse

        d = dict(src_dict)
        token = DeployTokenResponse.from_dict(d.pop("token"))

        token_plaintext = d.pop("token_plaintext")

        old_token_id = UUID(d.pop("old_token_id"))

        _old_token_expires_at = d.pop("old_token_expires_at", UNSET)
        old_token_expires_at: datetime.datetime | Unset
        if isinstance(_old_token_expires_at, Unset):
            old_token_expires_at = UNSET
        else:
            old_token_expires_at = datetime.datetime.fromisoformat(_old_token_expires_at)

        rotate_deploy_token_response = cls(
            token=token,
            token_plaintext=token_plaintext,
            old_token_id=old_token_id,
            old_token_expires_at=old_token_expires_at,
        )

        rotate_deploy_token_response.additional_properties = d
        return rotate_deploy_token_response

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

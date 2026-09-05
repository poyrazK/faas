from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_key_response_scopes_item import APIKeyResponseScopesItem, check_api_key_response_scopes_item
from ..models.api_key_response_status import APIKeyResponseStatus, check_api_key_response_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="APIKeyResponse")


@_attrs_define
class APIKeyResponse:
    """API key metadata: id, prefix (first 8 chars), label, scopes, created/last-used timestamps, request count.
    **Plaintext is returned only on POST**. `org_id` (PR 6 / issue #190 / IAM-6 / ADR-061) is the org the key was minted
    against; legacy account-scoped responses stamp `org_id = caller's personal org`. See `org.create_api_key` /
    `org.revoke_api_key` for the new org-scoped verbs.

        Example:
            {'id': '0123456789abcdef0123456789abcdef', 'org_id': '8b1f5e5d-273e-5a18-ae00-58fceba4fe6c', 'prefix':
                'fp_live_ab12cd34', 'label': 'ci-deploy', 'scopes': ['apps:read', 'deploy:write'], 'created_at':
                '2026-08-02T11:25:00Z', 'expires_at': '2027-08-02T11:25:00Z', 'status': 'active', 'last_used_at':
                '2026-07-25T13:25:00Z'}

    """

    id: str
    org_id: UUID
    """Org the key was minted against (PR 6). Always set — every `api_keys` row carries an org_id post-migration
    00127. Personal-org scoped keys stamp the caller's personal org id here."""
    prefix: str
    """First 16 chars of the key (e.g. `fp_live_abc12345…`)."""
    scopes: list[APIKeyResponseScopesItem]
    """Closed permission set attached to the key. storage:manage controls bucket lifecycle/grants; storage:read and
    storage:write also require a matching per-bucket grant; admin remains full access."""
    created_at: datetime.datetime
    label: None | str | Unset = UNSET
    last_used_at: datetime.datetime | None | Unset = UNSET
    expires_at: datetime.datetime | None | Unset = UNSET
    """When the key expires (RFC 3339). Absent on never-expiring admin keys."""
    status: APIKeyResponseStatus | Unset = UNSET
    """Status state machine. `active` = ready; `grace` = in the post-rotation window; `revoked` = terminal."""
    revoked_at: datetime.datetime | None | Unset = UNSET
    """When the key was revoked (RFC 3339). Absent on active/grace keys."""
    rotated_from_id: None | str | Unset = UNSET
    """Predecessor key id when this row was minted by rotateKey. Absent on a fresh mint."""
    plaintext: None | str | Unset = UNSET
    """PRESENT ONLY on POST /v1/keys and POST /v1/keys/{id}/rotate responses. Never returned again."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        org_id = str(self.org_id)

        prefix = self.prefix

        scopes = []
        for scopes_item_data in self.scopes:
            scopes_item: str = scopes_item_data
            scopes.append(scopes_item)

        created_at = self.created_at.isoformat()

        label: None | str | Unset
        if isinstance(self.label, Unset):
            label = UNSET
        else:
            label = self.label

        last_used_at: None | str | Unset
        if isinstance(self.last_used_at, Unset):
            last_used_at = UNSET
        elif isinstance(self.last_used_at, datetime.datetime):
            last_used_at = self.last_used_at.isoformat()
        else:
            last_used_at = self.last_used_at

        expires_at: None | str | Unset
        if isinstance(self.expires_at, Unset):
            expires_at = UNSET
        elif isinstance(self.expires_at, datetime.datetime):
            expires_at = self.expires_at.isoformat()
        else:
            expires_at = self.expires_at

        status: str | Unset = UNSET
        if not isinstance(self.status, Unset):
            status = self.status

        revoked_at: None | str | Unset
        if isinstance(self.revoked_at, Unset):
            revoked_at = UNSET
        elif isinstance(self.revoked_at, datetime.datetime):
            revoked_at = self.revoked_at.isoformat()
        else:
            revoked_at = self.revoked_at

        rotated_from_id: None | str | Unset
        if isinstance(self.rotated_from_id, Unset):
            rotated_from_id = UNSET
        else:
            rotated_from_id = self.rotated_from_id

        plaintext: None | str | Unset
        if isinstance(self.plaintext, Unset):
            plaintext = UNSET
        else:
            plaintext = self.plaintext

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "org_id": org_id,
                "prefix": prefix,
                "scopes": scopes,
                "created_at": created_at,
            }
        )
        if label is not UNSET:
            field_dict["label"] = label
        if last_used_at is not UNSET:
            field_dict["last_used_at"] = last_used_at
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if status is not UNSET:
            field_dict["status"] = status
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
        id = d.pop("id")

        org_id = UUID(d.pop("org_id"))

        prefix = d.pop("prefix")

        scopes = []
        _scopes = d.pop("scopes")
        for scopes_item_data in _scopes:
            scopes_item = check_api_key_response_scopes_item(scopes_item_data)

            scopes.append(scopes_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        def _parse_label(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        label = _parse_label(d.pop("label", UNSET))

        def _parse_last_used_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_used_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_used_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        last_used_at = _parse_last_used_at(d.pop("last_used_at", UNSET))

        def _parse_expires_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                expires_at_type_0 = datetime.datetime.fromisoformat(data)

                return expires_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        expires_at = _parse_expires_at(d.pop("expires_at", UNSET))

        _status = d.pop("status", UNSET)
        status: APIKeyResponseStatus | Unset
        if isinstance(_status, Unset):
            status = UNSET
        else:
            status = check_api_key_response_status(_status)

        def _parse_revoked_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                revoked_at_type_0 = datetime.datetime.fromisoformat(data)

                return revoked_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        revoked_at = _parse_revoked_at(d.pop("revoked_at", UNSET))

        def _parse_rotated_from_id(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        rotated_from_id = _parse_rotated_from_id(d.pop("rotated_from_id", UNSET))

        def _parse_plaintext(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        plaintext = _parse_plaintext(d.pop("plaintext", UNSET))

        api_key_response = cls(
            id=id,
            org_id=org_id,
            prefix=prefix,
            scopes=scopes,
            created_at=created_at,
            label=label,
            last_used_at=last_used_at,
            expires_at=expires_at,
            status=status,
            revoked_at=revoked_at,
            rotated_from_id=rotated_from_id,
            plaintext=plaintext,
        )

        api_key_response.additional_properties = d
        return api_key_response

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

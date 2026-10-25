from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.api_key_response import APIKeyResponse


T = TypeVar("T", bound="RotateOrgAPIKeyResponse")


@_attrs_define
class RotateOrgAPIKeyResponse:
    """Response body of POST /v1/orgs/{slug}/keys/{id}/rotate. Mirrors `RotateKeyResponse`; org-scoped because the request
    was — both keys carry the same `org_id` in `APIKeyResponse.org_id`. The new `key_plaintext` is returned exactly
    once; the old plaintext is NEVER returned.

    """

    key: APIKeyResponse
    """API key metadata: id, prefix (first 8 chars), label, scopes, created/last-used timestamps, request count.
    **Plaintext is returned only on POST**. `org_id` (PR 6 / issue #190 / IAM-6 / ADR-061) is the org the key was
    minted against; legacy account-scoped responses stamp `org_id = caller's personal org`. See `org.create_api_key`
    / `org.revoke_api_key` for the new org-scoped verbs."""
    key_plaintext: str
    """New plaintext (PRESENT ONLY on this response). Capture immediately; the API never returns it again. Org-
    scoped mint; the plaintext belongs to the active org's id."""
    old_key_id: str
    """Predecessor key id (matches Key.rotated_from_id). Same org as the new key (rotation is org-local, never re-
    bound)."""
    old_key_expires_at: datetime.datetime
    """Grace deadline applied to the old key (RFC 3339, UTC). When grace_window_days=0 the deadline is 'now'
    (atomic rotation). Mirrors the legacy `RotateKeyResponse.old_key_expires_at` contract; the org binding carries
    through in `APIKeyResponse.org_id`."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key.to_dict()

        key_plaintext = self.key_plaintext

        old_key_id = self.old_key_id

        old_key_expires_at = self.old_key_expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "key_plaintext": key_plaintext,
                "old_key_id": old_key_id,
                "old_key_expires_at": old_key_expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_key_response import APIKeyResponse

        d = dict(src_dict)
        key = APIKeyResponse.from_dict(d.pop("key"))

        key_plaintext = d.pop("key_plaintext")

        old_key_id = d.pop("old_key_id")

        old_key_expires_at = datetime.datetime.fromisoformat(d.pop("old_key_expires_at"))

        rotate_org_api_key_response = cls(
            key=key,
            key_plaintext=key_plaintext,
            old_key_id=old_key_id,
            old_key_expires_at=old_key_expires_at,
        )

        rotate_org_api_key_response.additional_properties = d
        return rotate_org_api_key_response

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

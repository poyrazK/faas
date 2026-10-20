from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AdminSetGithubWebhookSecretRequest")


@_attrs_define
class AdminSetGithubWebhookSecretRequest:
    """Body shape for POST /v1/admin/github-webhook-secrets
    (PR-D / ADR-012 §7 amendment). The CLI takes hex so the
    plaintext never has to be a binary argv value; the apid
    handler hex-decodes before the INSERT.

    """

    installation_id: int
    """GitHub App installation_id (positive bigint)."""
    secret_hex: str
    """Hex-encoded secret (16..64 bytes; 32..128 hex chars)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        installation_id = self.installation_id

        secret_hex = self.secret_hex

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "installation_id": installation_id,
                "secret_hex": secret_hex,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        installation_id = d.pop("installation_id")

        secret_hex = d.pop("secret_hex")

        admin_set_github_webhook_secret_request = cls(
            installation_id=installation_id,
            secret_hex=secret_hex,
        )

        admin_set_github_webhook_secret_request.additional_properties = d
        return admin_set_github_webhook_secret_request

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

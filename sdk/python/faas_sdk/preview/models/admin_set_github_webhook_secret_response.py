from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AdminSetGithubWebhookSecretResponse")


@_attrs_define
class AdminSetGithubWebhookSecretResponse:
    """Response shape for POST /v1/admin/github-webhook-secrets
    (PR-D / ADR-012 §7 amendment). upgraded_at is the
    post-upsert stamp — every successful call bumps it
    (the audit trail; an operator re-running with the same
    secret is itself a rotation event worth recording).

    """

    installation_id: int
    upgraded_at: datetime.datetime
    upgraded_by: str
    """admin:<account_id> or platform"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        installation_id = self.installation_id

        upgraded_at = self.upgraded_at.isoformat()

        upgraded_by = self.upgraded_by

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "installation_id": installation_id,
                "upgraded_at": upgraded_at,
                "upgraded_by": upgraded_by,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        installation_id = d.pop("installation_id")

        upgraded_at = datetime.datetime.fromisoformat(d.pop("upgraded_at"))

        upgraded_by = d.pop("upgraded_by")

        admin_set_github_webhook_secret_response = cls(
            installation_id=installation_id,
            upgraded_at=upgraded_at,
            upgraded_by=upgraded_by,
        )

        admin_set_github_webhook_secret_response.additional_properties = d
        return admin_set_github_webhook_secret_response

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

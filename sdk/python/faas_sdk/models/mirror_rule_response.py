from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="MirrorRuleResponse")


@_attrs_define
class MirrorRuleResponse:
    """A persisted mirror rule (issue #72 / ADR-125 / ADR-124 PR-A2).
    `always_stripped_headers` is rendered so the customer can audit
    what the gateway guarantees regardless of their
    `redact_headers` setting.

    """

    id: str
    account_id: str
    app_id: str
    source_deployment_id: UUID
    mirror_deployment_id: UUID
    percent: int
    enabled: bool
    include_body: bool
    allow_unsafe_methods: bool
    redact_headers: list[str]
    always_stripped_headers: list[str]
    """Headers the gateway ALWAYS strips regardless of the customer's redact_headers setting."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        account_id = self.account_id

        app_id = self.app_id

        source_deployment_id = str(self.source_deployment_id)

        mirror_deployment_id = str(self.mirror_deployment_id)

        percent = self.percent

        enabled = self.enabled

        include_body = self.include_body

        allow_unsafe_methods = self.allow_unsafe_methods

        redact_headers = self.redact_headers

        always_stripped_headers = self.always_stripped_headers

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "app_id": app_id,
                "source_deployment_id": source_deployment_id,
                "mirror_deployment_id": mirror_deployment_id,
                "percent": percent,
                "enabled": enabled,
                "include_body": include_body,
                "allow_unsafe_methods": allow_unsafe_methods,
                "redact_headers": redact_headers,
                "always_stripped_headers": always_stripped_headers,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        account_id = d.pop("account_id")

        app_id = d.pop("app_id")

        source_deployment_id = UUID(d.pop("source_deployment_id"))

        mirror_deployment_id = UUID(d.pop("mirror_deployment_id"))

        percent = d.pop("percent")

        enabled = d.pop("enabled")

        include_body = d.pop("include_body")

        allow_unsafe_methods = d.pop("allow_unsafe_methods")

        redact_headers = cast(list[str], d.pop("redact_headers"))

        always_stripped_headers = cast(list[str], d.pop("always_stripped_headers"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        mirror_rule_response = cls(
            id=id,
            account_id=account_id,
            app_id=app_id,
            source_deployment_id=source_deployment_id,
            mirror_deployment_id=mirror_deployment_id,
            percent=percent,
            enabled=enabled,
            include_body=include_body,
            allow_unsafe_methods=allow_unsafe_methods,
            redact_headers=redact_headers,
            always_stripped_headers=always_stripped_headers,
            created_at=created_at,
            updated_at=updated_at,
        )

        mirror_rule_response.additional_properties = d
        return mirror_rule_response

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

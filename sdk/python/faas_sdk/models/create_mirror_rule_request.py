from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateMirrorRuleRequest")


@_attrs_define
class CreateMirrorRuleRequest:
    """Body for POST /v1/apps/{slug}/mirrors. Both deployments must
    be `live` and belong to the same app. `include_body` defaults
    to `false`; enabling it stores only request/response SHA-256
    hashes for body-difference classification, never raw bodies.
    `redact_headers` is the
    customer's additive list on top of the always-stripped list
    (Authorization, Cookie, Set-Cookie, X-API-Key, Proxy-Authorization,
    WWW-Authenticate — applied by PR-A3's redaction layer, NOT by
    A2's storage layer).

    """

    source_deployment_id: UUID
    """Source deployment id (live; must belong to the slug's app)."""
    mirror_deployment_id: UUID
    """Mirror deployment id (live; must belong to the slug's app; must differ from source)."""
    percent: int | Unset = 100
    """Fan-out percent. 100 = mirror every customer request; lower = sampled shadow."""
    include_body: bool | Unset = False
    """If true, the comparison ledger captures request/response SHA-256 hashes for body-difference classification.
    Raw bodies are never stored. Off by default."""
    redact_headers: list[str] | Unset = UNSET
    """Customer-supplied additional header names to redact on top of the always-stripped list (Authorization,
    Cookie, Set-Cookie, X-API-Key, Proxy-Authorization, WWW-Authenticate)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source_deployment_id = str(self.source_deployment_id)

        mirror_deployment_id = str(self.mirror_deployment_id)

        percent = self.percent

        include_body = self.include_body

        redact_headers: list[str] | Unset = UNSET
        if not isinstance(self.redact_headers, Unset):
            redact_headers = self.redact_headers

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source_deployment_id": source_deployment_id,
                "mirror_deployment_id": mirror_deployment_id,
            }
        )
        if percent is not UNSET:
            field_dict["percent"] = percent
        if include_body is not UNSET:
            field_dict["include_body"] = include_body
        if redact_headers is not UNSET:
            field_dict["redact_headers"] = redact_headers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source_deployment_id = UUID(d.pop("source_deployment_id"))

        mirror_deployment_id = UUID(d.pop("mirror_deployment_id"))

        percent = d.pop("percent", UNSET)

        include_body = d.pop("include_body", UNSET)

        redact_headers = cast(list[str], d.pop("redact_headers", UNSET))

        create_mirror_rule_request = cls(
            source_deployment_id=source_deployment_id,
            mirror_deployment_id=mirror_deployment_id,
            percent=percent,
            include_body=include_body,
            redact_headers=redact_headers,
        )

        create_mirror_rule_request.additional_properties = d
        return create_mirror_rule_request

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

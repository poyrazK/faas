from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.put_data_upstream_request_kind import PutDataUpstreamRequestKind, check_put_data_upstream_request_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="PutDataUpstreamRequest")


@_attrs_define
class PutDataUpstreamRequest:
    """Upsert payload for a customer data upstream. The (kind, host, port,
    scope, deployment_scope) tuple is the deduplication key — repeating
    the PUT updates the existing row's `last_seen_at` and (if
    `FAAS_DATA_PLACEMENT=1`) the inferred-source tag. Plaintext host is
    never persisted; the on-disk column is `host_redacted_hash`.

    """

    kind: PutDataUpstreamRequestKind
    """Closed vocabulary (ADR-098 §D1). Adding a new kind requires an ADR."""
    host: str
    """RFC 952/1123 hostname (no IPv4). Hashed server-side; the hashed form is what's persisted."""
    port: int
    scope: str | Unset = UNSET
    """ADR-090 deployment-scope filter (1..40 chars, lowercase alnum + dash). Omitted = default scope."""
    deployment_scope: str | Unset = UNSET
    """ADR-098 amendment (issue #954) widens the dedupe key to include `deployment_scope` so staging-vs-prod
    upstreams don't collide on the same app. Same shape as `scope` (1..40 chars, lowercase alnum + dash). Omitted =
    default scope, the migration's SQL DEFAULT stamp."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        host = self.host

        port = self.port

        scope = self.scope

        deployment_scope = self.deployment_scope

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "host": host,
                "port": port,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope
        if deployment_scope is not UNSET:
            field_dict["deployment_scope"] = deployment_scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_put_data_upstream_request_kind(d.pop("kind"))

        host = d.pop("host")

        port = d.pop("port")

        scope = d.pop("scope", UNSET)

        deployment_scope = d.pop("deployment_scope", UNSET)

        put_data_upstream_request = cls(
            kind=kind,
            host=host,
            port=port,
            scope=scope,
            deployment_scope=deployment_scope,
        )

        put_data_upstream_request.additional_properties = d
        return put_data_upstream_request

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

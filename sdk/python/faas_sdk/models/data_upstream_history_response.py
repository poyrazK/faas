from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.data_upstream_history_response_kind import (
    DataUpstreamHistoryResponseKind,
    check_data_upstream_history_response_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_upstream_history_bucket import DataUpstreamHistoryBucket


T = TypeVar("T", bound="DataUpstreamHistoryResponse")


@_attrs_define
class DataUpstreamHistoryResponse:
    """Historical probe series for one redacted upstream and region."""

    host_redacted_hash: str
    """SHA-256 hex of (HostHashSalt||host); plaintext hosts never appear on this surface."""
    kind: DataUpstreamHistoryResponseKind
    port: int
    region: str
    buckets: list[DataUpstreamHistoryBucket]
    scope: str | Unset = UNSET
    """Env scope associated with the upstream."""
    deployment_scope: str | Unset = UNSET
    """Deployment scope from the ADR-098 issue #954 overlay."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        host_redacted_hash = self.host_redacted_hash

        kind: str = self.kind

        port = self.port

        region = self.region

        buckets = []
        for buckets_item_data in self.buckets:
            buckets_item = buckets_item_data.to_dict()
            buckets.append(buckets_item)

        scope = self.scope

        deployment_scope = self.deployment_scope

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "host_redacted_hash": host_redacted_hash,
                "kind": kind,
                "port": port,
                "region": region,
                "buckets": buckets,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope
        if deployment_scope is not UNSET:
            field_dict["deployment_scope"] = deployment_scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_upstream_history_bucket import DataUpstreamHistoryBucket

        d = dict(src_dict)
        host_redacted_hash = d.pop("host_redacted_hash")

        kind = check_data_upstream_history_response_kind(d.pop("kind"))

        port = d.pop("port")

        region = d.pop("region")

        buckets = []
        _buckets = d.pop("buckets")
        for buckets_item_data in _buckets:
            buckets_item = DataUpstreamHistoryBucket.from_dict(buckets_item_data)

            buckets.append(buckets_item)

        scope = d.pop("scope", UNSET)

        deployment_scope = d.pop("deployment_scope", UNSET)

        data_upstream_history_response = cls(
            host_redacted_hash=host_redacted_hash,
            kind=kind,
            port=port,
            region=region,
            buckets=buckets,
            scope=scope,
            deployment_scope=deployment_scope,
        )

        data_upstream_history_response.additional_properties = d
        return data_upstream_history_response

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

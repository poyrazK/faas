from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="KafkaTLSConfig")


@_attrs_define
class KafkaTLSConfig:
    """Kafka TLS material. MinVersion is forced to TLS 1.2 at
    decoder time regardless of what the wire sends
    (pkg/sched/poller_kafka.go::buildKafkaTLSConfig). When
    ClientCert + ClientKey are both set the decoder performs
    a half-wired guard — if only one is set, decode returns
    an error rather than the poller falling through silently
    to PLAINTEXT over an apparent mTLS endpoint.

    """

    ca_cert: str | Unset = UNSET
    """PEM-encoded CA bundle. Optional; if omitted the system trust store is used."""
    client_cert: str | Unset = UNSET
    """PEM-encoded client cert for mTLS."""
    client_key: str | Unset = UNSET
    """PEM-encoded client key for mTLS, accepted only on writes. Omit inside a supplied TLS block to preserve the
    stored key."""
    client_key_set: bool | Unset = UNSET
    """Response-only marker indicating that a client key is configured; no credential material is returned."""
    skip_verify: bool | Unset = False
    """Skip TLS verification. Hobby plan rejects this
    (TLSSkipVerifyAllowed=false in pkg/api/limits.go);
    Pro and Scale accept it for self-signed brokers.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ca_cert = self.ca_cert

        client_cert = self.client_cert

        client_key = self.client_key

        client_key_set = self.client_key_set

        skip_verify = self.skip_verify

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if ca_cert is not UNSET:
            field_dict["ca_cert"] = ca_cert
        if client_cert is not UNSET:
            field_dict["client_cert"] = client_cert
        if client_key is not UNSET:
            field_dict["client_key"] = client_key
        if client_key_set is not UNSET:
            field_dict["client_key_set"] = client_key_set
        if skip_verify is not UNSET:
            field_dict["skip_verify"] = skip_verify

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ca_cert = d.pop("ca_cert", UNSET)

        client_cert = d.pop("client_cert", UNSET)

        client_key = d.pop("client_key", UNSET)

        client_key_set = d.pop("client_key_set", UNSET)

        skip_verify = d.pop("skip_verify", UNSET)

        kafka_tls_config = cls(
            ca_cert=ca_cert,
            client_cert=client_cert,
            client_key=client_key,
            client_key_set=client_key_set,
            skip_verify=skip_verify,
        )

        kafka_tls_config.additional_properties = d
        return kafka_tls_config

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

from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.kafka_sasl_config import KafkaSASLConfig
    from ..models.kafka_tls_config import KafkaTLSConfig


T = TypeVar("T", bound="KafkaTriggerConfig")


@_attrs_define
class KafkaTriggerConfig:
    """Decoded `config` for kind=kafka triggers. The wire-level
    blob lives in Trigger.config; this is the SDK's
    server-side shape.

    """

    brokers: list[str]
    """Bootstrap broker list (host:port per entry)."""
    topic: str
    group: str
    tls: KafkaTLSConfig | Unset = UNSET
    """Kafka TLS material. MinVersion is forced to TLS 1.2 at
    decoder time regardless of what the wire sends
    (pkg/sched/poller_kafka.go::buildKafkaTLSConfig). When
    ClientCert + ClientKey are both set the decoder performs
    a half-wired guard — if only one is set, decode returns
    an error rather than the poller falling through silently
    to PLAINTEXT over an apparent mTLS endpoint.
    """
    sasl: KafkaSASLConfig | Unset = UNSET
    """Kafka SASL credentials. Required Username + Password for
    every supported mechanism. xdg-go/scram library derives
    SCRAM client keypairs from Username + Password at dial
    time.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        brokers = self.brokers

        topic = self.topic

        group = self.group

        tls: dict[str, Any] | Unset = UNSET
        if not isinstance(self.tls, Unset):
            tls = self.tls.to_dict()

        sasl: dict[str, Any] | Unset = UNSET
        if not isinstance(self.sasl, Unset):
            sasl = self.sasl.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "brokers": brokers,
                "topic": topic,
                "group": group,
            }
        )
        if tls is not UNSET:
            field_dict["tls"] = tls
        if sasl is not UNSET:
            field_dict["sasl"] = sasl

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.kafka_sasl_config import KafkaSASLConfig
        from ..models.kafka_tls_config import KafkaTLSConfig

        d = dict(src_dict)
        brokers = cast(list[str], d.pop("brokers"))

        topic = d.pop("topic")

        group = d.pop("group")

        _tls = d.pop("tls", UNSET)
        tls: KafkaTLSConfig | Unset
        if isinstance(_tls, Unset):
            tls = UNSET
        else:
            tls = KafkaTLSConfig.from_dict(_tls)

        _sasl = d.pop("sasl", UNSET)
        sasl: KafkaSASLConfig | Unset
        if isinstance(_sasl, Unset):
            sasl = UNSET
        else:
            sasl = KafkaSASLConfig.from_dict(_sasl)

        kafka_trigger_config = cls(
            brokers=brokers,
            topic=topic,
            group=group,
            tls=tls,
            sasl=sasl,
        )

        kafka_trigger_config.additional_properties = d
        return kafka_trigger_config

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

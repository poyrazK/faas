from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.kafka_sasl_mechanism import KafkaSASLMechanism, check_kafka_sasl_mechanism
from ..types import UNSET, Unset

T = TypeVar("T", bound="KafkaSASLConfig")


@_attrs_define
class KafkaSASLConfig:
    """Kafka SASL credentials. Required Username + Password for
    every supported mechanism. xdg-go/scram library derives
    SCRAM client keypairs from Username + Password at dial
    time.

    """

    mechanism: KafkaSASLMechanism
    """Kafka SASL mechanism (ADR-118 §5). Closed-vocab."""
    username: str
    password: str | Unset = UNSET
    """Plaintext-on-write only. Omit inside a supplied SASL block to preserve the stored password."""
    password_set: bool | Unset = UNSET
    """Response-only marker indicating that a password is configured; no credential material is returned."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mechanism: str = self.mechanism

        username = self.username

        password = self.password

        password_set = self.password_set

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mechanism": mechanism,
                "username": username,
            }
        )
        if password is not UNSET:
            field_dict["password"] = password
        if password_set is not UNSET:
            field_dict["password_set"] = password_set

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mechanism = check_kafka_sasl_mechanism(d.pop("mechanism"))

        username = d.pop("username")

        password = d.pop("password", UNSET)

        password_set = d.pop("password_set", UNSET)

        kafka_sasl_config = cls(
            mechanism=mechanism,
            username=username,
            password=password,
            password_set=password_set,
        )

        kafka_sasl_config.additional_properties = d
        return kafka_sasl_config

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

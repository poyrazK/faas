from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dns_record_instruction_purpose import DNSRecordInstructionPurpose, check_dns_record_instruction_purpose
from ..models.dns_record_instruction_type import DNSRecordInstructionType, check_dns_record_instruction_type
from ..types import UNSET, Unset

T = TypeVar("T", bound="DNSRecordInstruction")


@_attrs_define
class DNSRecordInstruction:
    """One DNS record a customer publishes for a custom domain (ADR-520). `alternative` marks an A/AAAA routing record that
    replaces the CNAME where a CNAME is not allowed, such as at a zone apex.

    """

    type_: DNSRecordInstructionType
    name: str
    value: str
    purpose: DNSRecordInstructionPurpose
    alternative: bool | Unset = False
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_: str = self.type_

        name = self.name

        value = self.value

        purpose: str = self.purpose

        alternative = self.alternative

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "name": name,
                "value": value,
                "purpose": purpose,
            }
        )
        if alternative is not UNSET:
            field_dict["alternative"] = alternative

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        type_ = check_dns_record_instruction_type(d.pop("type"))

        name = d.pop("name")

        value = d.pop("value")

        purpose = check_dns_record_instruction_purpose(d.pop("purpose"))

        alternative = d.pop("alternative", UNSET)

        dns_record_instruction = cls(
            type_=type_,
            name=name,
            value=value,
            purpose=purpose,
            alternative=alternative,
        )

        dns_record_instruction.additional_properties = d
        return dns_record_instruction

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

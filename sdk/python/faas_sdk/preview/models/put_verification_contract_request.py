from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.put_verification_contract_request_contract import PutVerificationContractRequestContract


T = TypeVar("T", bound="PutVerificationContractRequest")


@_attrs_define
class PutVerificationContractRequest:
    """Body that stores or replaces an app's verification contract."""

    contract: PutVerificationContractRequestContract
    """The gregale.verify.yaml contract to store, as JSON (version, checks)."""
    expected_revision: int | Unset = UNSET
    """0 creates only; a positive value replaces only that revision."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        contract = self.contract.to_dict()

        expected_revision = self.expected_revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "contract": contract,
            }
        )
        if expected_revision is not UNSET:
            field_dict["expected_revision"] = expected_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.put_verification_contract_request_contract import PutVerificationContractRequestContract

        d = dict(src_dict)
        contract = PutVerificationContractRequestContract.from_dict(d.pop("contract"))

        expected_revision = d.pop("expected_revision", UNSET)

        put_verification_contract_request = cls(
            contract=contract,
            expected_revision=expected_revision,
        )

        put_verification_contract_request.additional_properties = d
        return put_verification_contract_request

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

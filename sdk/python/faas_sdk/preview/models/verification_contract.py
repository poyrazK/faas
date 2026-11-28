from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.verification_contract_contract import VerificationContractContract


T = TypeVar("T", bound="VerificationContract")


@_attrs_define
class VerificationContract:
    """An app's stored gregale.verify.yaml contract and its revision."""

    app_id: UUID
    contract: VerificationContractContract
    """The stored gregale.verify.yaml contract as JSON (version, checks)."""
    contract_sha256: str
    """SHA-256 of the canonical contract encoding."""
    check_count: int
    revision: int
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        contract = self.contract.to_dict()

        contract_sha256 = self.contract_sha256

        check_count = self.check_count

        revision = self.revision

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "contract": contract,
                "contract_sha256": contract_sha256,
                "check_count": check_count,
                "revision": revision,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.verification_contract_contract import VerificationContractContract

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        contract = VerificationContractContract.from_dict(d.pop("contract"))

        contract_sha256 = d.pop("contract_sha256")

        check_count = d.pop("check_count")

        revision = d.pop("revision")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        verification_contract = cls(
            app_id=app_id,
            contract=contract,
            contract_sha256=contract_sha256,
            check_count=check_count,
            revision=revision,
            updated_at=updated_at,
        )

        verification_contract.additional_properties = d
        return verification_contract

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

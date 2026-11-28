from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ConfigRollbackRequest")


@_attrs_define
class ConfigRollbackRequest:
    """The config revision whose variables a rollback restores."""

    revision: int
    expected_revision: int | Unset = UNSET
    """Current configuration revision reviewed before rollback."""
    expected_app_id: UUID | Unset = UNSET
    """Immutable reviewed app identity."""
    expected_environment_id: UUID | Unset = UNSET
    """Immutable reviewed named environment identity."""
    request_id: UUID | Unset = UNSET
    """Original reviewed rollback request identity."""
    receipt_proof: str | Unset = UNSET
    """Private proof required for receipt recovery."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        expected_revision = self.expected_revision

        expected_app_id: str | Unset = UNSET
        if not isinstance(self.expected_app_id, Unset):
            expected_app_id = str(self.expected_app_id)

        expected_environment_id: str | Unset = UNSET
        if not isinstance(self.expected_environment_id, Unset):
            expected_environment_id = str(self.expected_environment_id)

        request_id: str | Unset = UNSET
        if not isinstance(self.request_id, Unset):
            request_id = str(self.request_id)

        receipt_proof = self.receipt_proof

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "revision": revision,
            }
        )
        if expected_revision is not UNSET:
            field_dict["expected_revision"] = expected_revision
        if expected_app_id is not UNSET:
            field_dict["expected_app_id"] = expected_app_id
        if expected_environment_id is not UNSET:
            field_dict["expected_environment_id"] = expected_environment_id
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if receipt_proof is not UNSET:
            field_dict["receipt_proof"] = receipt_proof

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        revision = d.pop("revision")

        expected_revision = d.pop("expected_revision", UNSET)

        _expected_app_id = d.pop("expected_app_id", UNSET)
        expected_app_id: UUID | Unset
        if isinstance(_expected_app_id, Unset):
            expected_app_id = UNSET
        else:
            expected_app_id = UUID(_expected_app_id)

        _expected_environment_id = d.pop("expected_environment_id", UNSET)
        expected_environment_id: UUID | Unset
        if isinstance(_expected_environment_id, Unset):
            expected_environment_id = UNSET
        else:
            expected_environment_id = UUID(_expected_environment_id)

        _request_id = d.pop("request_id", UNSET)
        request_id: UUID | Unset
        if isinstance(_request_id, Unset):
            request_id = UNSET
        else:
            request_id = UUID(_request_id)

        receipt_proof = d.pop("receipt_proof", UNSET)

        config_rollback_request = cls(
            revision=revision,
            expected_revision=expected_revision,
            expected_app_id=expected_app_id,
            expected_environment_id=expected_environment_id,
            request_id=request_id,
            receipt_proof=receipt_proof,
        )

        config_rollback_request.additional_properties = d
        return config_rollback_request

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

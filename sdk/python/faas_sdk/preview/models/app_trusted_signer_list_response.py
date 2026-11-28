from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.trusted_signer import TrustedSigner


T = TypeVar("T", bound="AppTrustedSignerListResponse")


@_attrs_define
class AppTrustedSignerListResponse:
    """GET /v1/apps/{slug}/trusted_signers response body. Empty list is the EXPECTED state for any app with
    require_signed=false.

    """

    signers: list[TrustedSigner]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        signers = []
        for signers_item_data in self.signers:
            signers_item = signers_item_data.to_dict()
            signers.append(signers_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "signers": signers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.trusted_signer import TrustedSigner

        d = dict(src_dict)
        signers = []
        _signers = d.pop("signers")
        for signers_item_data in _signers:
            signers_item = TrustedSigner.from_dict(signers_item_data)

            signers.append(signers_item)

        app_trusted_signer_list_response = cls(
            signers=signers,
        )

        app_trusted_signer_list_response.additional_properties = d
        return app_trusted_signer_list_response

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

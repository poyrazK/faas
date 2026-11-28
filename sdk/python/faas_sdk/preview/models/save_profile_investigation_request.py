from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.profile_investigation_input import ProfileInvestigationInput


T = TypeVar("T", bound="SaveProfileInvestigationRequest")


@_attrs_define
class SaveProfileInvestigationRequest:
    """Complete replacement. Supply expected_revision 0 for creation or the current revision for updates. Concurrent
    changes return conflict. Body limited to 65536 bytes.

    """

    expected_revision: int
    investigation: ProfileInvestigationInput
    """Saved comparison metadata. Title allows 160 UTF-8 bytes and findings and notes 8192 bytes each. Selections
    share a runtime. Changed windows must belong to retained deployments; unchanged expired windows permit
    commentary edits."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        investigation = self.investigation.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_revision": expected_revision,
                "investigation": investigation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_investigation_input import ProfileInvestigationInput

        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        investigation = ProfileInvestigationInput.from_dict(d.pop("investigation"))

        save_profile_investigation_request = cls(
            expected_revision=expected_revision,
            investigation=investigation,
        )

        save_profile_investigation_request.additional_properties = d
        return save_profile_investigation_request

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

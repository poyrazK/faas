from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.labels_response_labels import LabelsResponseLabels


T = TypeVar("T", bound="LabelsResponse")


@_attrs_define
class LabelsResponse:
    """Every label the resource carries after a write."""

    labels: LabelsResponseLabels
    """Key/value labels with Kubernetes syntax, at most 32. A key is an optional DNS-subdomain prefix and a slash,
    then a name of 1-63 letters, digits, '.', '_' or '-' that starts and ends with a letter or digit; a value is
    empty or 1-63 of the same. Keys under gregale.dev/ belong to Gregale. Labels are not secret: they appear in
    lists and audit events."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        labels = self.labels.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "labels": labels,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.labels_response_labels import LabelsResponseLabels

        d = dict(src_dict)
        labels = LabelsResponseLabels.from_dict(d.pop("labels"))

        labels_response = cls(
            labels=labels,
        )

        labels_response.additional_properties = d
        return labels_response

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

from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_secret_reference_list_response_references import AppSecretReferenceListResponseReferences


T = TypeVar("T", bound="AppSecretReferenceListResponse")


@_attrs_define
class AppSecretReferenceListResponse:
    """Current environment identity, destination-to-source names, suppressed destination keys and shared variable/reference
    quota usage.

    """

    environment_id: UUID
    environment: str
    references: AppSecretReferenceListResponseReferences
    count: int
    """Shared variable/reference quota usage across all environments."""
    quota: int
    suppressed_keys: list[str] | Unset = UNSET
    """Destinations excluded from primary workload secret delivery until an explicit PUT reference re-enables them.
    Omitted by older servers; consumes no variable slots."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        environment_id = str(self.environment_id)

        environment = self.environment

        references = self.references.to_dict()

        count = self.count

        quota = self.quota

        suppressed_keys: list[str] | Unset = UNSET
        if not isinstance(self.suppressed_keys, Unset):
            suppressed_keys = self.suppressed_keys

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "environment_id": environment_id,
                "environment": environment,
                "references": references,
                "count": count,
                "quota": quota,
            }
        )
        if suppressed_keys is not UNSET:
            field_dict["suppressed_keys"] = suppressed_keys

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_secret_reference_list_response_references import AppSecretReferenceListResponseReferences

        d = dict(src_dict)
        environment_id = UUID(d.pop("environment_id"))

        environment = d.pop("environment")

        references = AppSecretReferenceListResponseReferences.from_dict(d.pop("references"))

        count = d.pop("count")

        quota = d.pop("quota")

        suppressed_keys = cast(list[str], d.pop("suppressed_keys", UNSET))

        app_secret_reference_list_response = cls(
            environment_id=environment_id,
            environment=environment,
            references=references,
            count=count,
            quota=quota,
            suppressed_keys=suppressed_keys,
        )

        app_secret_reference_list_response.additional_properties = d
        return app_secret_reference_list_response

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

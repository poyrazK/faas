from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.issue_tracker_provider_status import IssueTrackerProviderStatus


T = TypeVar("T", bound="NativeIssueTrackerStatus")


@_attrs_define
class NativeIssueTrackerStatus:
    """Verified native connection and observed status of a linked tracker issue."""

    issue_id: UUID
    link_id: UUID
    installation_id: int
    repository: str
    repository_id: int
    status: IssueTrackerProviderStatus
    """Observed lifecycle and identity of an external tracker issue."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        issue_id = str(self.issue_id)

        link_id = str(self.link_id)

        installation_id = self.installation_id

        repository = self.repository

        repository_id = self.repository_id

        status = self.status.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "issue_id": issue_id,
                "link_id": link_id,
                "installation_id": installation_id,
                "repository": repository,
                "repository_id": repository_id,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_tracker_provider_status import IssueTrackerProviderStatus

        d = dict(src_dict)
        issue_id = UUID(d.pop("issue_id"))

        link_id = UUID(d.pop("link_id"))

        installation_id = d.pop("installation_id")

        repository = d.pop("repository")

        repository_id = d.pop("repository_id")

        status = IssueTrackerProviderStatus.from_dict(d.pop("status"))

        native_issue_tracker_status = cls(
            issue_id=issue_id,
            link_id=link_id,
            installation_id=installation_id,
            repository=repository,
            repository_id=repository_id,
            status=status,
        )

        native_issue_tracker_status.additional_properties = d
        return native_issue_tracker_status

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

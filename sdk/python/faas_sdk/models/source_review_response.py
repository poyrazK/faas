from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.app_release_review import AppReleaseReview


T = TypeVar("T", bound="SourceReviewResponse")


@_attrs_define
class SourceReviewResponse:
    """Resolved repository identity and immutable commit bound to a release review."""

    app_id: UUID
    repo: str
    commit_sha: str
    installation_id: int
    repository_id: int
    source_root: str
    release_review: AppReleaseReview
    """Atomic app configuration and serving-deployment identities used to fence release admission."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        repo = self.repo

        commit_sha = self.commit_sha

        installation_id = self.installation_id

        repository_id = self.repository_id

        source_root = self.source_root

        release_review = self.release_review.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "repo": repo,
                "commit_sha": commit_sha,
                "installation_id": installation_id,
                "repository_id": repository_id,
                "source_root": source_root,
                "release_review": release_review,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_release_review import AppReleaseReview

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        repo = d.pop("repo")

        commit_sha = d.pop("commit_sha")

        installation_id = d.pop("installation_id")

        repository_id = d.pop("repository_id")

        source_root = d.pop("source_root")

        release_review = AppReleaseReview.from_dict(d.pop("release_review"))

        source_review_response = cls(
            app_id=app_id,
            repo=repo,
            commit_sha=commit_sha,
            installation_id=installation_id,
            repository_id=repository_id,
            source_root=source_root,
            release_review=release_review,
        )

        source_review_response.additional_properties = d
        return source_review_response

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

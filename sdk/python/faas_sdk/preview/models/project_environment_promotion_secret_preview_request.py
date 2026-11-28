from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.project_environment_promotion_secret_preview_request_secrets import (
        ProjectEnvironmentPromotionSecretPreviewRequestSecrets,
    )


T = TypeVar("T", bound="ProjectEnvironmentPromotionSecretPreviewRequest")


@_attrs_define
class ProjectEnvironmentPromotionSecretPreviewRequest:
    """Environment promotion preview inputs referencing staged immutable secrets without plaintext values."""

    from_environment: str
    sync_config: bool
    verify: bool
    secrets: ProjectEnvironmentPromotionSecretPreviewRequestSecrets
    """Complete destination secret key/version maps by workload. Values must be staged immutable UUIDs, never
    plaintext."""

    def to_dict(self) -> dict[str, Any]:
        from_environment = self.from_environment

        sync_config = self.sync_config

        verify = self.verify

        secrets = self.secrets.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "from_environment": from_environment,
                "sync_config": sync_config,
                "verify": verify,
                "secrets": secrets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_promotion_secret_preview_request_secrets import (
            ProjectEnvironmentPromotionSecretPreviewRequestSecrets,
        )

        d = dict(src_dict)
        from_environment = d.pop("from_environment")

        sync_config = d.pop("sync_config")

        verify = d.pop("verify")

        secrets = ProjectEnvironmentPromotionSecretPreviewRequestSecrets.from_dict(d.pop("secrets"))

        project_environment_promotion_secret_preview_request = cls(
            from_environment=from_environment,
            sync_config=sync_config,
            verify=verify,
            secrets=secrets,
        )

        return project_environment_promotion_secret_preview_request

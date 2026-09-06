from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.upload_deploy_options import UploadDeployOptions


T = TypeVar("T", bound="UploadStartRequest")


@_attrs_define
class UploadStartRequest:
    """Body of POST /v1/uploads. `total_size` must be ≤ the per-plan SourceTarballMaxMB cap (Free/Hobby 100 MB, Pro/Scale
    250 MB); the handler returns 413 + `source_too_large` otherwise. `sha256_hex` is recorded for the build_provenance
    audit row only — the server does NOT re-verify it at commit time (ADR-115 trust boundary).

    """

    app_slug: str
    total_size: int
    """Total tarball bytes after gzip. Hard ceiling is 1 GiB; per-plan cap is enforced by the handler."""
    sha256_hex: None | str | Unset = UNSET
    """Optional sha256 of the tarball for build_provenance. Server does not verify."""
    deploy_options: UploadDeployOptions | Unset = UNSET
    """Deployment metadata persisted with the upload session and applied at commit."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        total_size = self.total_size

        sha256_hex: None | str | Unset
        if isinstance(self.sha256_hex, Unset):
            sha256_hex = UNSET
        else:
            sha256_hex = self.sha256_hex

        deploy_options: dict[str, Any] | Unset = UNSET
        if not isinstance(self.deploy_options, Unset):
            deploy_options = self.deploy_options.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_slug": app_slug,
                "total_size": total_size,
            }
        )
        if sha256_hex is not UNSET:
            field_dict["sha256_hex"] = sha256_hex
        if deploy_options is not UNSET:
            field_dict["deploy_options"] = deploy_options

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.upload_deploy_options import UploadDeployOptions

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        total_size = d.pop("total_size")

        def _parse_sha256_hex(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        sha256_hex = _parse_sha256_hex(d.pop("sha256_hex", UNSET))

        _deploy_options = d.pop("deploy_options", UNSET)
        deploy_options: UploadDeployOptions | Unset
        if isinstance(_deploy_options, Unset):
            deploy_options = UNSET
        else:
            deploy_options = UploadDeployOptions.from_dict(_deploy_options)

        upload_start_request = cls(
            app_slug=app_slug,
            total_size=total_size,
            sha256_hex=sha256_hex,
            deploy_options=deploy_options,
        )

        upload_start_request.additional_properties = d
        return upload_start_request

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

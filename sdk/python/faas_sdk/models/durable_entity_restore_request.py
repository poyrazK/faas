from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.durable_entity_state_export import DurableEntityStateExport


T = TypeVar("T", bound="DurableEntityRestoreRequest")


@_attrs_define
class DurableEntityRestoreRequest:
    """Candidate export and comparison identity used for restore preview, validation or fenced publication."""

    namespace: str
    key: str
    request_id: str
    expected_version: int
    export: DurableEntityStateExport
    """Checksummed committed application data with immutable entity identity; excludes recovery and delivery
    history."""
    environment: str | Unset = UNSET
    platform_tenant_id: str | Unset = UNSET
    validation_bundle_sha256: str | Unset = UNSET
    """Registered validator bundle digest. Required for isolated restores."""
    validation_deployment_id: UUID | Unset = UNSET
    """Explicit deployment pin. Required for restores when application validation is enabled."""

    def to_dict(self) -> dict[str, Any]:
        namespace = self.namespace

        key = self.key

        request_id = self.request_id

        expected_version = self.expected_version

        export = self.export.to_dict()

        environment = self.environment

        platform_tenant_id = self.platform_tenant_id

        validation_bundle_sha256 = self.validation_bundle_sha256

        validation_deployment_id: str | Unset = UNSET
        if not isinstance(self.validation_deployment_id, Unset):
            validation_deployment_id = str(self.validation_deployment_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "namespace": namespace,
                "key": key,
                "request_id": request_id,
                "expected_version": expected_version,
                "export": export,
            }
        )
        if environment is not UNSET:
            field_dict["environment"] = environment
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id
        if validation_bundle_sha256 is not UNSET:
            field_dict["validation_bundle_sha256"] = validation_bundle_sha256
        if validation_deployment_id is not UNSET:
            field_dict["validation_deployment_id"] = validation_deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.durable_entity_state_export import DurableEntityStateExport

        d = dict(src_dict)
        namespace = d.pop("namespace")

        key = d.pop("key")

        request_id = d.pop("request_id")

        expected_version = d.pop("expected_version")

        export = DurableEntityStateExport.from_dict(d.pop("export"))

        environment = d.pop("environment", UNSET)

        platform_tenant_id = d.pop("platform_tenant_id", UNSET)

        validation_bundle_sha256 = d.pop("validation_bundle_sha256", UNSET)

        _validation_deployment_id = d.pop("validation_deployment_id", UNSET)
        validation_deployment_id: UUID | Unset
        if isinstance(_validation_deployment_id, Unset):
            validation_deployment_id = UNSET
        else:
            validation_deployment_id = UUID(_validation_deployment_id)

        durable_entity_restore_request = cls(
            namespace=namespace,
            key=key,
            request_id=request_id,
            expected_version=expected_version,
            export=export,
            environment=environment,
            platform_tenant_id=platform_tenant_id,
            validation_bundle_sha256=validation_bundle_sha256,
            validation_deployment_id=validation_deployment_id,
        )

        return durable_entity_restore_request

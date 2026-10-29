from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.route_requirements_report import RouteRequirementsReport


T = TypeVar("T", bound="RouteRequirementsCheck")


@_attrs_define
class RouteRequirementsCheck:
    """Read-only coverage against one captured deployment and current configuration with saved intent provenance. Does not
    persist history or prove runtime behavior.

    """

    version: int
    app: str
    app_id: UUID
    deployment_id: UUID
    requirements_revision: int
    requirements_sha256: str
    configuration_sha256: str
    report: RouteRequirementsReport
    """Configuration evidence for concrete requests or every captured operation with group assignments and bounded
    policy scope."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        app = self.app

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        requirements_revision = self.requirements_revision

        requirements_sha256 = self.requirements_sha256

        configuration_sha256 = self.configuration_sha256

        report = self.report.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "app": app,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "requirements_revision": requirements_revision,
                "requirements_sha256": requirements_sha256,
                "configuration_sha256": configuration_sha256,
                "report": report,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_requirements_report import RouteRequirementsReport

        d = dict(src_dict)
        version = d.pop("version")

        app = d.pop("app")

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        requirements_revision = d.pop("requirements_revision")

        requirements_sha256 = d.pop("requirements_sha256")

        configuration_sha256 = d.pop("configuration_sha256")

        report = RouteRequirementsReport.from_dict(d.pop("report"))

        route_requirements_check = cls(
            version=version,
            app=app,
            app_id=app_id,
            deployment_id=deployment_id,
            requirements_revision=requirements_revision,
            requirements_sha256=requirements_sha256,
            configuration_sha256=configuration_sha256,
            report=report,
        )

        route_requirements_check.additional_properties = d
        return route_requirements_check

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

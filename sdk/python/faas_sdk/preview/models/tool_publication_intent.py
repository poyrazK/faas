from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_publication_intent_access_mode import (
    ToolPublicationIntentAccessMode,
    check_tool_publication_intent_access_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.tool_publication_intent_tool_scopes import ToolPublicationIntentToolScopes


T = TypeVar("T", bound="ToolPublicationIntent")


@_attrs_define
class ToolPublicationIntent:
    """Reviewed publication metadata bounded to 64 KiB; never includes local paths or credentials."""

    app: str
    source_sha256: str
    """Reviewed local source-tree and mode digest"""
    fixtures_sha256: str
    access_mode: ToolPublicationIntentAccessMode
    tool_scopes: ToolPublicationIntentToolScopes
    test_cases: list[str]
    endpoint_path: str | Unset = UNSET
    """Reviewed canonical MCP path; new invocations require this field and never infer a missing endpoint"""
    app_id: str | Unset = UNSET
    expected_serving_deployment_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        source_sha256 = self.source_sha256

        fixtures_sha256 = self.fixtures_sha256

        access_mode: str = self.access_mode

        tool_scopes = self.tool_scopes.to_dict()

        test_cases = self.test_cases

        endpoint_path = self.endpoint_path

        app_id = self.app_id

        expected_serving_deployment_id = self.expected_serving_deployment_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "source_sha256": source_sha256,
                "fixtures_sha256": fixtures_sha256,
                "access_mode": access_mode,
                "tool_scopes": tool_scopes,
                "test_cases": test_cases,
            }
        )
        if endpoint_path is not UNSET:
            field_dict["endpoint_path"] = endpoint_path
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if expected_serving_deployment_id is not UNSET:
            field_dict["expected_serving_deployment_id"] = expected_serving_deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tool_publication_intent_tool_scopes import ToolPublicationIntentToolScopes

        d = dict(src_dict)
        app = d.pop("app")

        source_sha256 = d.pop("source_sha256")

        fixtures_sha256 = d.pop("fixtures_sha256")

        access_mode = check_tool_publication_intent_access_mode(d.pop("access_mode"))

        tool_scopes = ToolPublicationIntentToolScopes.from_dict(d.pop("tool_scopes"))

        test_cases = cast(list[str], d.pop("test_cases"))

        endpoint_path = d.pop("endpoint_path", UNSET)

        app_id = d.pop("app_id", UNSET)

        expected_serving_deployment_id = d.pop("expected_serving_deployment_id", UNSET)

        tool_publication_intent = cls(
            app=app,
            source_sha256=source_sha256,
            fixtures_sha256=fixtures_sha256,
            access_mode=access_mode,
            tool_scopes=tool_scopes,
            test_cases=test_cases,
            endpoint_path=endpoint_path,
            app_id=app_id,
            expected_serving_deployment_id=expected_serving_deployment_id,
        )

        tool_publication_intent.additional_properties = d
        return tool_publication_intent

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

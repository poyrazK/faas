from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.api_sandbox_suite import APISandboxSuite


T = TypeVar("T", bound="APISandboxSuiteResponse")


@_attrs_define
class APISandboxSuiteResponse:
    """The app's saved suite, or null when the app has no suite."""

    suite: APISandboxSuite | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.api_sandbox_suite import APISandboxSuite

        suite: dict[str, Any] | None
        if isinstance(self.suite, APISandboxSuite):
            suite = self.suite.to_dict()
        else:
            suite = self.suite

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "suite": suite,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_suite import APISandboxSuite

        d = dict(src_dict)

        def _parse_suite(data: object) -> APISandboxSuite | None:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                suite_type_0 = APISandboxSuite.from_dict(data)

                return suite_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(APISandboxSuite | None, data)

        suite = _parse_suite(d.pop("suite"))

        api_sandbox_suite_response = cls(
            suite=suite,
        )

        api_sandbox_suite_response.additional_properties = d
        return api_sandbox_suite_response

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

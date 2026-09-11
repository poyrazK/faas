from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deployment_change_field import DeploymentChangeField, check_deployment_change_field

if TYPE_CHECKING:
    from ..models.deployment_change_after_type_3 import DeploymentChangeAfterType3
    from ..models.deployment_change_before_type_3 import DeploymentChangeBeforeType3


T = TypeVar("T", bound="DeploymentChange")


@_attrs_define
class DeploymentChange:
    """One non-secret release field that changed from the previous deployment."""

    field: DeploymentChangeField
    before: bool | DeploymentChangeBeforeType3 | float | None | str
    after: bool | DeploymentChangeAfterType3 | float | None | str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.deployment_change_after_type_3 import DeploymentChangeAfterType3
        from ..models.deployment_change_before_type_3 import DeploymentChangeBeforeType3

        field: str = self.field

        before: bool | dict[str, Any] | float | None | str
        if isinstance(self.before, DeploymentChangeBeforeType3):
            before = self.before.to_dict()
        else:
            before = self.before

        after: bool | dict[str, Any] | float | None | str
        if isinstance(self.after, DeploymentChangeAfterType3):
            after = self.after.to_dict()
        else:
            after = self.after

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "field": field,
                "before": before,
                "after": after,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deployment_change_after_type_3 import DeploymentChangeAfterType3
        from ..models.deployment_change_before_type_3 import DeploymentChangeBeforeType3

        d = dict(src_dict)
        field = check_deployment_change_field(d.pop("field"))

        def _parse_before(data: object) -> bool | DeploymentChangeBeforeType3 | float | None | str:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                before_type_3 = DeploymentChangeBeforeType3.from_dict(data)

                return before_type_3
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(bool | DeploymentChangeBeforeType3 | float | None | str, data)

        before = _parse_before(d.pop("before"))

        def _parse_after(data: object) -> bool | DeploymentChangeAfterType3 | float | None | str:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                after_type_3 = DeploymentChangeAfterType3.from_dict(data)

                return after_type_3
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(bool | DeploymentChangeAfterType3 | float | None | str, data)

        after = _parse_after(d.pop("after"))

        deployment_change = cls(
            field=field,
            before=before,
            after=after,
        )

        deployment_change.additional_properties = d
        return deployment_change

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

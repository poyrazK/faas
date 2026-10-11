from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.managed_operation_result_gregale_operation_result import (
    ManagedOperationResultGregaleOperationResult,
    check_managed_operation_result_gregale_operation_result,
)

if TYPE_CHECKING:
    from ..models.managed_operation_effect import ManagedOperationEffect


T = TypeVar("T", bound="ManagedOperationResult")


@_attrs_define
class ManagedOperationResult:
    """Opt-in managed HTTP operation handler response; encode as JSON and return from the handler after requiring
    X-Gregale-Operation-Result-Version header value 1. The entire response is bounded to 1 MiB. Ordinary invocation
    responses are unchanged. Only supported on managed request operations, including Commit operations.

    """

    gregale_operation_result: ManagedOperationResultGregaleOperationResult
    result: Any
    """Business result persisted atomically with effect enqueue; any JSON value"""
    effects: list[ManagedOperationEffect]

    def to_dict(self) -> dict[str, Any]:
        gregale_operation_result: int = self.gregale_operation_result

        result = self.result

        effects = []
        for effects_item_data in self.effects:
            effects_item = effects_item_data.to_dict()
            effects.append(effects_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "gregale_operation_result": gregale_operation_result,
                "result": result,
                "effects": effects,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_operation_effect import ManagedOperationEffect

        d = dict(src_dict)
        gregale_operation_result = check_managed_operation_result_gregale_operation_result(
            d.pop("gregale_operation_result")
        )

        result = d.pop("result")

        effects = []
        _effects = d.pop("effects")
        for effects_item_data in _effects:
            effects_item = ManagedOperationEffect.from_dict(effects_item_data)

            effects.append(effects_item)

        managed_operation_result = cls(
            gregale_operation_result=gregale_operation_result,
            result=result,
            effects=effects,
        )

        return managed_operation_result

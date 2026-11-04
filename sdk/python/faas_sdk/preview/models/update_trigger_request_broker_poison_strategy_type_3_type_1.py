from typing import Literal

UpdateTriggerRequestBrokerPoisonStrategyType3Type1 = Literal["commit", "seek-to-offset"]

UPDATE_TRIGGER_REQUEST_BROKER_POISON_STRATEGY_TYPE_3_TYPE_1_VALUES: set[
    UpdateTriggerRequestBrokerPoisonStrategyType3Type1
] = {
    "commit",
    "seek-to-offset",
}


def check_update_trigger_request_broker_poison_strategy_type_3_type_1(
    value: str,
) -> UpdateTriggerRequestBrokerPoisonStrategyType3Type1:
    if value in UPDATE_TRIGGER_REQUEST_BROKER_POISON_STRATEGY_TYPE_3_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_TRIGGER_REQUEST_BROKER_POISON_STRATEGY_TYPE_3_TYPE_1_VALUES!r}"
    )

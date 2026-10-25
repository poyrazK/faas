from typing import Literal

UpdateTriggerRequestBrokerPoisonStrategyType1 = Literal["commit", "seek-to-offset"]

UPDATE_TRIGGER_REQUEST_BROKER_POISON_STRATEGY_TYPE_1_VALUES: set[UpdateTriggerRequestBrokerPoisonStrategyType1] = {
    "commit",
    "seek-to-offset",
}


def check_update_trigger_request_broker_poison_strategy_type_1(
    value: str,
) -> UpdateTriggerRequestBrokerPoisonStrategyType1:
    if value in UPDATE_TRIGGER_REQUEST_BROKER_POISON_STRATEGY_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_TRIGGER_REQUEST_BROKER_POISON_STRATEGY_TYPE_1_VALUES!r}"
    )

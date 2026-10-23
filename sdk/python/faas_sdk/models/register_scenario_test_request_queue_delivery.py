from typing import Literal

RegisterScenarioTestRequestQueueDelivery = Literal["same_run"]

REGISTER_SCENARIO_TEST_REQUEST_QUEUE_DELIVERY_VALUES: set[RegisterScenarioTestRequestQueueDelivery] = {
    "same_run",
}


def check_register_scenario_test_request_queue_delivery(value: str) -> RegisterScenarioTestRequestQueueDelivery:
    if value in REGISTER_SCENARIO_TEST_REQUEST_QUEUE_DELIVERY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REGISTER_SCENARIO_TEST_REQUEST_QUEUE_DELIVERY_VALUES!r}"
    )

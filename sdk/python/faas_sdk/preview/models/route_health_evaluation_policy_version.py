from typing import Literal

RouteHealthEvaluationPolicyVersion = Literal[1]

ROUTE_HEALTH_EVALUATION_POLICY_VERSION_VALUES: set[RouteHealthEvaluationPolicyVersion] = {
    1,
}


def check_route_health_evaluation_policy_version(value: int) -> RouteHealthEvaluationPolicyVersion:
    if value in ROUTE_HEALTH_EVALUATION_POLICY_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_EVALUATION_POLICY_VERSION_VALUES!r}")

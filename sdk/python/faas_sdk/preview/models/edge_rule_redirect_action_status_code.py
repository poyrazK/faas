from typing import Literal

EdgeRuleRedirectActionStatusCode = Literal[301, 302, 307, 308]

EDGE_RULE_REDIRECT_ACTION_STATUS_CODE_VALUES: set[EdgeRuleRedirectActionStatusCode] = {
    301,
    302,
    307,
    308,
}


def check_edge_rule_redirect_action_status_code(value: int) -> EdgeRuleRedirectActionStatusCode:
    if value in EDGE_RULE_REDIRECT_ACTION_STATUS_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_REDIRECT_ACTION_STATUS_CODE_VALUES!r}")

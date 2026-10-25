from typing import Literal

UpdateAppRequestCrawlerPolicyType3Type1 = Literal["block", "cached", "wake"]

UPDATE_APP_REQUEST_CRAWLER_POLICY_TYPE_3_TYPE_1_VALUES: set[UpdateAppRequestCrawlerPolicyType3Type1] = {
    "block",
    "cached",
    "wake",
}


def check_update_app_request_crawler_policy_type_3_type_1(value: str) -> UpdateAppRequestCrawlerPolicyType3Type1:
    if value in UPDATE_APP_REQUEST_CRAWLER_POLICY_TYPE_3_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_CRAWLER_POLICY_TYPE_3_TYPE_1_VALUES!r}"
    )

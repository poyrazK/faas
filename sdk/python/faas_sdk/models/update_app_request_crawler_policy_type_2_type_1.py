from typing import Literal

UpdateAppRequestCrawlerPolicyType2Type1 = Literal["block", "cached", "wake"]

UPDATE_APP_REQUEST_CRAWLER_POLICY_TYPE_2_TYPE_1_VALUES: set[UpdateAppRequestCrawlerPolicyType2Type1] = {
    "block",
    "cached",
    "wake",
}


def check_update_app_request_crawler_policy_type_2_type_1(value: str) -> UpdateAppRequestCrawlerPolicyType2Type1:
    if value in UPDATE_APP_REQUEST_CRAWLER_POLICY_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_CRAWLER_POLICY_TYPE_2_TYPE_1_VALUES!r}"
    )

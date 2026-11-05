from typing import Literal

EdgeWAFReviewGroupAssessment = Literal["excluded", "likely_attack", "likely_false_positive", "review"]

EDGE_WAF_REVIEW_GROUP_ASSESSMENT_VALUES: set[EdgeWAFReviewGroupAssessment] = {
    "excluded",
    "likely_attack",
    "likely_false_positive",
    "review",
}


def check_edge_waf_review_group_assessment(value: str) -> EdgeWAFReviewGroupAssessment:
    if value in EDGE_WAF_REVIEW_GROUP_ASSESSMENT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_WAF_REVIEW_GROUP_ASSESSMENT_VALUES!r}")

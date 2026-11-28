from typing import Literal

AccountRiskLinkSignal = Literal["email_domain", "signup_network"]

ACCOUNT_RISK_LINK_SIGNAL_VALUES: set[AccountRiskLinkSignal] = {
    "email_domain",
    "signup_network",
}


def check_account_risk_link_signal(value: str) -> AccountRiskLinkSignal:
    if value in ACCOUNT_RISK_LINK_SIGNAL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ACCOUNT_RISK_LINK_SIGNAL_VALUES!r}")

from typing import Literal

IssueBrowserCSRFTokenAction = Literal[
    "auth.logout",
    "auth.session.revoke",
    "auth.sessions.revoke_all",
    "mfa_confirm",
    "mfa_disable",
    "mfa_recover",
    "set_password",
]

ISSUE_BROWSER_CSRF_TOKEN_ACTION_VALUES: set[IssueBrowserCSRFTokenAction] = {
    "auth.logout",
    "auth.session.revoke",
    "auth.sessions.revoke_all",
    "mfa_confirm",
    "mfa_disable",
    "mfa_recover",
    "set_password",
}


def check_issue_browser_csrf_token_action(value: str) -> IssueBrowserCSRFTokenAction:
    if value in ISSUE_BROWSER_CSRF_TOKEN_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_BROWSER_CSRF_TOKEN_ACTION_VALUES!r}")

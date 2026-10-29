from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.issue_compound_alert_policy import IssueCompoundAlertPolicy
from ...models.problem import Problem
from ...models.update_issue_compound_alert_policy_request import UpdateIssueCompoundAlertPolicyRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: UpdateIssueCompoundAlertPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/issue-compound-alert-policy".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> IssueCompoundAlertPolicy | Problem | None:
    if response.status_code == 200:
        response_200 = IssueCompoundAlertPolicy.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[IssueCompoundAlertPolicy | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: UpdateIssueCompoundAlertPolicyRequest,
) -> Response[IssueCompoundAlertPolicy | Problem]:
    """Replace application issue event-compound rules.

     Replaces up to ten named AND rules using expected_revision. Both event count and verified distinct
    customer count must meet their thresholds in the same (now-window, now] retained-event window,
    optionally scoped to one environment. Active ignores and snoozes suppress admission. Only a new
    accepted capture inside the window triggers evaluation; retries, configuration changes and expiry
    alone do not notify. Cooldowns are per rule and canonical issue, survive enable toggles, and reset
    for changed conditions or timing. Activity, webhook issue.compound_threshold_reached and native
    Slack admission commit with the capture. Delivery requires an enabled subscription or eligible team
    destination. Frequency and fixed-window customer-impact policies remain independent.

    Args:
        slug (str):
        body (UpdateIssueCompoundAlertPolicyRequest): Replacement compound-alert rules guarded by
            the expected policy revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IssueCompoundAlertPolicy | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: UpdateIssueCompoundAlertPolicyRequest,
) -> IssueCompoundAlertPolicy | Problem | None:
    """Replace application issue event-compound rules.

     Replaces up to ten named AND rules using expected_revision. Both event count and verified distinct
    customer count must meet their thresholds in the same (now-window, now] retained-event window,
    optionally scoped to one environment. Active ignores and snoozes suppress admission. Only a new
    accepted capture inside the window triggers evaluation; retries, configuration changes and expiry
    alone do not notify. Cooldowns are per rule and canonical issue, survive enable toggles, and reset
    for changed conditions or timing. Activity, webhook issue.compound_threshold_reached and native
    Slack admission commit with the capture. Delivery requires an enabled subscription or eligible team
    destination. Frequency and fixed-window customer-impact policies remain independent.

    Args:
        slug (str):
        body (UpdateIssueCompoundAlertPolicyRequest): Replacement compound-alert rules guarded by
            the expected policy revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IssueCompoundAlertPolicy | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: UpdateIssueCompoundAlertPolicyRequest,
) -> Response[IssueCompoundAlertPolicy | Problem]:
    """Replace application issue event-compound rules.

     Replaces up to ten named AND rules using expected_revision. Both event count and verified distinct
    customer count must meet their thresholds in the same (now-window, now] retained-event window,
    optionally scoped to one environment. Active ignores and snoozes suppress admission. Only a new
    accepted capture inside the window triggers evaluation; retries, configuration changes and expiry
    alone do not notify. Cooldowns are per rule and canonical issue, survive enable toggles, and reset
    for changed conditions or timing. Activity, webhook issue.compound_threshold_reached and native
    Slack admission commit with the capture. Delivery requires an enabled subscription or eligible team
    destination. Frequency and fixed-window customer-impact policies remain independent.

    Args:
        slug (str):
        body (UpdateIssueCompoundAlertPolicyRequest): Replacement compound-alert rules guarded by
            the expected policy revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IssueCompoundAlertPolicy | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: UpdateIssueCompoundAlertPolicyRequest,
) -> IssueCompoundAlertPolicy | Problem | None:
    """Replace application issue event-compound rules.

     Replaces up to ten named AND rules using expected_revision. Both event count and verified distinct
    customer count must meet their thresholds in the same (now-window, now] retained-event window,
    optionally scoped to one environment. Active ignores and snoozes suppress admission. Only a new
    accepted capture inside the window triggers evaluation; retries, configuration changes and expiry
    alone do not notify. Cooldowns are per rule and canonical issue, survive enable toggles, and reset
    for changed conditions or timing. Activity, webhook issue.compound_threshold_reached and native
    Slack admission commit with the capture. Delivery requires an enabled subscription or eligible team
    destination. Frequency and fixed-window customer-impact policies remain independent.

    Args:
        slug (str):
        body (UpdateIssueCompoundAlertPolicyRequest): Replacement compound-alert rules guarded by
            the expected policy revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IssueCompoundAlertPolicy | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed

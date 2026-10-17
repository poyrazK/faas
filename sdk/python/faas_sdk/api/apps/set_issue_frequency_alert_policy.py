from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.issue_frequency_alert_policy import IssueFrequencyAlertPolicy
from ...models.problem import Problem
from ...models.update_issue_frequency_alert_policy_request import UpdateIssueFrequencyAlertPolicyRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: UpdateIssueFrequencyAlertPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/issue-frequency-alert-policy".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> IssueFrequencyAlertPolicy | Problem | None:
    if response.status_code == 200:
        response_200 = IssueFrequencyAlertPolicy.from_dict(response.json())

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
) -> Response[IssueFrequencyAlertPolicy | Problem]:
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
    body: UpdateIssueFrequencyAlertPolicyRequest,
) -> Response[IssueFrequencyAlertPolicy | Problem]:
    """Replace application issue event-frequency rules.

     Requires the current revision (zero initially). Replaces up to ten named rules; an empty array
    removes all rules. Stale writes return 409. Counts retained accepted occurrences per canonical issue
    in (now-window, now]. Only a new capture inside the window evaluates a rule. At or above threshold,
    the first eligible capture after cooldown alerts again. Active ignore periods suppress alerts while
    collection continues. Configuration edits do not send notifications. Changing a rule's environment,
    threshold, window or cooldown starts a fresh cycle on the next capture; unchanged rules and
    disable/re-enable retain cooldowns. Webhook event is issue.frequency_threshold_reached. This policy
    does not alter customer-impact alerts.

    Args:
        slug (str):
        body (UpdateIssueFrequencyAlertPolicyRequest): Replacement frequency-alert rules guarded
            by the expected policy revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IssueFrequencyAlertPolicy | Problem]
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
    body: UpdateIssueFrequencyAlertPolicyRequest,
) -> IssueFrequencyAlertPolicy | Problem | None:
    """Replace application issue event-frequency rules.

     Requires the current revision (zero initially). Replaces up to ten named rules; an empty array
    removes all rules. Stale writes return 409. Counts retained accepted occurrences per canonical issue
    in (now-window, now]. Only a new capture inside the window evaluates a rule. At or above threshold,
    the first eligible capture after cooldown alerts again. Active ignore periods suppress alerts while
    collection continues. Configuration edits do not send notifications. Changing a rule's environment,
    threshold, window or cooldown starts a fresh cycle on the next capture; unchanged rules and
    disable/re-enable retain cooldowns. Webhook event is issue.frequency_threshold_reached. This policy
    does not alter customer-impact alerts.

    Args:
        slug (str):
        body (UpdateIssueFrequencyAlertPolicyRequest): Replacement frequency-alert rules guarded
            by the expected policy revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IssueFrequencyAlertPolicy | Problem
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
    body: UpdateIssueFrequencyAlertPolicyRequest,
) -> Response[IssueFrequencyAlertPolicy | Problem]:
    """Replace application issue event-frequency rules.

     Requires the current revision (zero initially). Replaces up to ten named rules; an empty array
    removes all rules. Stale writes return 409. Counts retained accepted occurrences per canonical issue
    in (now-window, now]. Only a new capture inside the window evaluates a rule. At or above threshold,
    the first eligible capture after cooldown alerts again. Active ignore periods suppress alerts while
    collection continues. Configuration edits do not send notifications. Changing a rule's environment,
    threshold, window or cooldown starts a fresh cycle on the next capture; unchanged rules and
    disable/re-enable retain cooldowns. Webhook event is issue.frequency_threshold_reached. This policy
    does not alter customer-impact alerts.

    Args:
        slug (str):
        body (UpdateIssueFrequencyAlertPolicyRequest): Replacement frequency-alert rules guarded
            by the expected policy revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IssueFrequencyAlertPolicy | Problem]
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
    body: UpdateIssueFrequencyAlertPolicyRequest,
) -> IssueFrequencyAlertPolicy | Problem | None:
    """Replace application issue event-frequency rules.

     Requires the current revision (zero initially). Replaces up to ten named rules; an empty array
    removes all rules. Stale writes return 409. Counts retained accepted occurrences per canonical issue
    in (now-window, now]. Only a new capture inside the window evaluates a rule. At or above threshold,
    the first eligible capture after cooldown alerts again. Active ignore periods suppress alerts while
    collection continues. Configuration edits do not send notifications. Changing a rule's environment,
    threshold, window or cooldown starts a fresh cycle on the next capture; unchanged rules and
    disable/re-enable retain cooldowns. Webhook event is issue.frequency_threshold_reached. This policy
    does not alter customer-impact alerts.

    Args:
        slug (str):
        body (UpdateIssueFrequencyAlertPolicyRequest): Replacement frequency-alert rules guarded
            by the expected policy revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IssueFrequencyAlertPolicy | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed

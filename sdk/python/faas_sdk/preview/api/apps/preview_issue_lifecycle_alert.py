from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.issue_lifecycle_alert_preview import IssueLifecycleAlertPreview
from ...models.preview_issue_lifecycle_alert_request import PreviewIssueLifecycleAlertRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: PreviewIssueLifecycleAlertRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/issue-lifecycle-alert-policy/preview".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> IssueLifecycleAlertPreview | Problem | None:
    if response.status_code == 200:
        response_200 = IssueLifecycleAlertPreview.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[IssueLifecycleAlertPreview | Problem]:
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
    body: PreviewIssueLifecycleAlertRequest,
) -> Response[IssueLifecycleAlertPreview | Problem]:
    """Preview a draft lifecycle alert filter without sending.

     Requires app read access and Issues entitlement. Evaluates the supplied draft's environment filter
    and shared cooldown for a hypothetical created/regressed event, resolving merged aliases. Reads one
    database snapshot and returns the saved policy revision at that snapshot. Does not save, reserve
    cooldown, mutate lifecycle or contact Slack. Allowed means this policy permits the event; actual
    delivery separately applies native event subscription, ignore/response state, eligible routing,
    capacity and sender gates.

    Args:
        slug (str):
        body (PreviewIssueLifecycleAlertRequest): Draft policy and hypothetical lifecycle event
            for an existing issue.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IssueLifecycleAlertPreview | Problem]
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
    body: PreviewIssueLifecycleAlertRequest,
) -> IssueLifecycleAlertPreview | Problem | None:
    """Preview a draft lifecycle alert filter without sending.

     Requires app read access and Issues entitlement. Evaluates the supplied draft's environment filter
    and shared cooldown for a hypothetical created/regressed event, resolving merged aliases. Reads one
    database snapshot and returns the saved policy revision at that snapshot. Does not save, reserve
    cooldown, mutate lifecycle or contact Slack. Allowed means this policy permits the event; actual
    delivery separately applies native event subscription, ignore/response state, eligible routing,
    capacity and sender gates.

    Args:
        slug (str):
        body (PreviewIssueLifecycleAlertRequest): Draft policy and hypothetical lifecycle event
            for an existing issue.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IssueLifecycleAlertPreview | Problem
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
    body: PreviewIssueLifecycleAlertRequest,
) -> Response[IssueLifecycleAlertPreview | Problem]:
    """Preview a draft lifecycle alert filter without sending.

     Requires app read access and Issues entitlement. Evaluates the supplied draft's environment filter
    and shared cooldown for a hypothetical created/regressed event, resolving merged aliases. Reads one
    database snapshot and returns the saved policy revision at that snapshot. Does not save, reserve
    cooldown, mutate lifecycle or contact Slack. Allowed means this policy permits the event; actual
    delivery separately applies native event subscription, ignore/response state, eligible routing,
    capacity and sender gates.

    Args:
        slug (str):
        body (PreviewIssueLifecycleAlertRequest): Draft policy and hypothetical lifecycle event
            for an existing issue.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IssueLifecycleAlertPreview | Problem]
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
    body: PreviewIssueLifecycleAlertRequest,
) -> IssueLifecycleAlertPreview | Problem | None:
    """Preview a draft lifecycle alert filter without sending.

     Requires app read access and Issues entitlement. Evaluates the supplied draft's environment filter
    and shared cooldown for a hypothetical created/regressed event, resolving merged aliases. Reads one
    database snapshot and returns the saved policy revision at that snapshot. Does not save, reserve
    cooldown, mutate lifecycle or contact Slack. Allowed means this policy permits the event; actual
    delivery separately applies native event subscription, ignore/response state, eligible routing,
    capacity and sender gates.

    Args:
        slug (str):
        body (PreviewIssueLifecycleAlertRequest): Draft policy and hypothetical lifecycle event
            for an existing issue.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IssueLifecycleAlertPreview | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed

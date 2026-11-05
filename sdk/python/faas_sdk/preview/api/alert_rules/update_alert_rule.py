from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.alert_rule_response import AlertRuleResponse
from ...models.problem import Problem
from ...models.update_alert_rule_request import UpdateAlertRuleRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    *,
    body: UpdateAlertRuleRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/apps/{slug}/alerts/{id}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AlertRuleResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AlertRuleResponse.from_dict(response.json())

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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AlertRuleResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAlertRuleRequest,
) -> Response[AlertRuleResponse | Problem]:
    """Partial-update an alert rule.

     Every field is optional. metric cannot cross families
    (e.g. error_rate_pct → failed_invocations) — returns
    400 alert_rule_invalid.

    Args:
        slug (str):
        id (str):
        body (UpdateAlertRuleRequest): Partial update — every field is optional. Omitted means
            leave alone.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AlertRuleResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAlertRuleRequest,
) -> AlertRuleResponse | Problem | None:
    """Partial-update an alert rule.

     Every field is optional. metric cannot cross families
    (e.g. error_rate_pct → failed_invocations) — returns
    400 alert_rule_invalid.

    Args:
        slug (str):
        id (str):
        body (UpdateAlertRuleRequest): Partial update — every field is optional. Omitted means
            leave alone.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AlertRuleResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAlertRuleRequest,
) -> Response[AlertRuleResponse | Problem]:
    """Partial-update an alert rule.

     Every field is optional. metric cannot cross families
    (e.g. error_rate_pct → failed_invocations) — returns
    400 alert_rule_invalid.

    Args:
        slug (str):
        id (str):
        body (UpdateAlertRuleRequest): Partial update — every field is optional. Omitted means
            leave alone.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AlertRuleResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAlertRuleRequest,
) -> AlertRuleResponse | Problem | None:
    """Partial-update an alert rule.

     Every field is optional. metric cannot cross families
    (e.g. error_rate_pct → failed_invocations) — returns
    400 alert_rule_invalid.

    Args:
        slug (str):
        id (str):
        body (UpdateAlertRuleRequest): Partial update — every field is optional. Omitted means
            leave alone.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AlertRuleResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed

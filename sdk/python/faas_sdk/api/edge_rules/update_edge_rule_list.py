from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.edge_rule_list_response import EdgeRuleListResponse
from ...models.problem import Problem
from ...models.update_edge_rule_list_request import UpdateEdgeRuleListRequest
from ...types import Response


def _get_kwargs(
    name: str,
    *,
    body: UpdateEdgeRuleListRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/edge-rule-lists/{name}".format(
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EdgeRuleListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EdgeRuleListResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
) -> Response[EdgeRuleListResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateEdgeRuleListRequest,
) -> Response[EdgeRuleListResponse | Problem]:
    """Edit an edge-rule list.

     ADR-907. items replaces the list; add and remove edit it in place
    (remove applies after add) and cannot be combined with items.
    Gateways pick up the change for every referencing rule within a few
    seconds; no rule-set version is recorded.

    Args:
        name (str):
        body (UpdateEdgeRuleListRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeRuleListResponse | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateEdgeRuleListRequest,
) -> EdgeRuleListResponse | Problem | None:
    """Edit an edge-rule list.

     ADR-907. items replaces the list; add and remove edit it in place
    (remove applies after add) and cannot be combined with items.
    Gateways pick up the change for every referencing rule within a few
    seconds; no rule-set version is recorded.

    Args:
        name (str):
        body (UpdateEdgeRuleListRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeRuleListResponse | Problem
    """

    return sync_detailed(
        name=name,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateEdgeRuleListRequest,
) -> Response[EdgeRuleListResponse | Problem]:
    """Edit an edge-rule list.

     ADR-907. items replaces the list; add and remove edit it in place
    (remove applies after add) and cannot be combined with items.
    Gateways pick up the change for every referencing rule within a few
    seconds; no rule-set version is recorded.

    Args:
        name (str):
        body (UpdateEdgeRuleListRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeRuleListResponse | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateEdgeRuleListRequest,
) -> EdgeRuleListResponse | Problem | None:
    """Edit an edge-rule list.

     ADR-907. items replaces the list; add and remove edit it in place
    (remove applies after add) and cannot be combined with items.
    Gateways pick up the change for every referencing rule within a few
    seconds; no rule-set version is recorded.

    Args:
        name (str):
        body (UpdateEdgeRuleListRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeRuleListResponse | Problem
    """

    return (
        await asyncio_detailed(
            name=name,
            client=client,
            body=body,
        )
    ).parsed

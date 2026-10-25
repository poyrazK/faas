from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.edge_rule_response import EdgeRuleResponse
from ...models.edge_waf_exclusions_request import EdgeWAFExclusionsRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    body: EdgeWAFExclusionsRequest,
    if_match: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(if_match, Unset):
        headers["If-Match"] = if_match

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/edge-rules/{id}/waf-exclusions".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EdgeRuleResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EdgeRuleResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 412:
        response_412 = Problem.from_dict(response.json())

        return response_412

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EdgeRuleResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: EdgeWAFExclusionsRequest,
    if_match: str | Unset = UNSET,
) -> Response[EdgeRuleResponse | Problem]:
    """Add or remove CRS rule IDs on a kind=waf rule's exclusions.

     ADR-831 amendment 13. Changes exclude_rule_ids and keeps the rest of
    the rule's action and its penalty, so silencing a false positive does
    not mean restating the whole action. Recorded as a new rule-set
    version like any PATCH.

    Args:
        id (str):
        if_match (str | Unset):
        body (EdgeWAFExclusionsRequest): CRS rule IDs to add to and remove from a kind=waf rule's
            exclude_rule_ids (ADR-831 amendment 13).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeRuleResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        if_match=if_match,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: EdgeWAFExclusionsRequest,
    if_match: str | Unset = UNSET,
) -> EdgeRuleResponse | Problem | None:
    """Add or remove CRS rule IDs on a kind=waf rule's exclusions.

     ADR-831 amendment 13. Changes exclude_rule_ids and keeps the rest of
    the rule's action and its penalty, so silencing a false positive does
    not mean restating the whole action. Recorded as a new rule-set
    version like any PATCH.

    Args:
        id (str):
        if_match (str | Unset):
        body (EdgeWAFExclusionsRequest): CRS rule IDs to add to and remove from a kind=waf rule's
            exclude_rule_ids (ADR-831 amendment 13).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeRuleResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        if_match=if_match,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: EdgeWAFExclusionsRequest,
    if_match: str | Unset = UNSET,
) -> Response[EdgeRuleResponse | Problem]:
    """Add or remove CRS rule IDs on a kind=waf rule's exclusions.

     ADR-831 amendment 13. Changes exclude_rule_ids and keeps the rest of
    the rule's action and its penalty, so silencing a false positive does
    not mean restating the whole action. Recorded as a new rule-set
    version like any PATCH.

    Args:
        id (str):
        if_match (str | Unset):
        body (EdgeWAFExclusionsRequest): CRS rule IDs to add to and remove from a kind=waf rule's
            exclude_rule_ids (ADR-831 amendment 13).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeRuleResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        if_match=if_match,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: EdgeWAFExclusionsRequest,
    if_match: str | Unset = UNSET,
) -> EdgeRuleResponse | Problem | None:
    """Add or remove CRS rule IDs on a kind=waf rule's exclusions.

     ADR-831 amendment 13. Changes exclude_rule_ids and keeps the rest of
    the rule's action and its penalty, so silencing a false positive does
    not mean restating the whole action. Recorded as a new rule-set
    version like any PATCH.

    Args:
        id (str):
        if_match (str | Unset):
        body (EdgeWAFExclusionsRequest): CRS rule IDs to add to and remove from a kind=waf rule's
            exclude_rule_ids (ADR-831 amendment 13).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeRuleResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            if_match=if_match,
        )
    ).parsed

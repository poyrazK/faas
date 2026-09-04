from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.advance_canary_request import AdvanceCanaryRequest
from ...models.canary_advance_response import CanaryAdvanceResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: AdvanceCanaryRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/deployments/{id}/canary/advance".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CanaryAdvanceResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CanaryAdvanceResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CanaryAdvanceResponse | Problem]:
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
    body: AdvanceCanaryRequest,
) -> Response[CanaryAdvanceResponse | Problem]:
    """Advance one persisted canary stage.

     Atomically advances the deployment's canary ladder by one stage.
    The caller supplies the canary_step it observed; APID resolves the
    next percentage from the deployment's persisted preset and rejects
    stale workers with `409 canary_step_conflict`. The state transition,
    sibling traffic rebalance, terminal promotion, and deployment audit
    row are committed together. Pro/Scale only — Free/Hobby are rejected
    at 403 `plan_traffic_split_not_allowed`.

    Args:
        id (str):
        body (AdvanceCanaryRequest): Body for POST /v1/deployments/{id}/canary/advance (issue #976
            / ADR-122). expected_step is the persisted canary step observed by the progression worker;
            APID derives the next traffic percentage and rejects stale observations.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CanaryAdvanceResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: AdvanceCanaryRequest,
) -> CanaryAdvanceResponse | Problem | None:
    """Advance one persisted canary stage.

     Atomically advances the deployment's canary ladder by one stage.
    The caller supplies the canary_step it observed; APID resolves the
    next percentage from the deployment's persisted preset and rejects
    stale workers with `409 canary_step_conflict`. The state transition,
    sibling traffic rebalance, terminal promotion, and deployment audit
    row are committed together. Pro/Scale only — Free/Hobby are rejected
    at 403 `plan_traffic_split_not_allowed`.

    Args:
        id (str):
        body (AdvanceCanaryRequest): Body for POST /v1/deployments/{id}/canary/advance (issue #976
            / ADR-122). expected_step is the persisted canary step observed by the progression worker;
            APID derives the next traffic percentage and rejects stale observations.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CanaryAdvanceResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: AdvanceCanaryRequest,
) -> Response[CanaryAdvanceResponse | Problem]:
    """Advance one persisted canary stage.

     Atomically advances the deployment's canary ladder by one stage.
    The caller supplies the canary_step it observed; APID resolves the
    next percentage from the deployment's persisted preset and rejects
    stale workers with `409 canary_step_conflict`. The state transition,
    sibling traffic rebalance, terminal promotion, and deployment audit
    row are committed together. Pro/Scale only — Free/Hobby are rejected
    at 403 `plan_traffic_split_not_allowed`.

    Args:
        id (str):
        body (AdvanceCanaryRequest): Body for POST /v1/deployments/{id}/canary/advance (issue #976
            / ADR-122). expected_step is the persisted canary step observed by the progression worker;
            APID derives the next traffic percentage and rejects stale observations.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CanaryAdvanceResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: AdvanceCanaryRequest,
) -> CanaryAdvanceResponse | Problem | None:
    """Advance one persisted canary stage.

     Atomically advances the deployment's canary ladder by one stage.
    The caller supplies the canary_step it observed; APID resolves the
    next percentage from the deployment's persisted preset and rejects
    stale workers with `409 canary_step_conflict`. The state transition,
    sibling traffic rebalance, terminal promotion, and deployment audit
    row are committed together. Pro/Scale only — Free/Hobby are rejected
    at 403 `plan_traffic_split_not_allowed`.

    Args:
        id (str):
        body (AdvanceCanaryRequest): Body for POST /v1/deployments/{id}/canary/advance (issue #976
            / ADR-122). expected_step is the persisted canary step observed by the progression worker;
            APID derives the next traffic percentage and rejects stale observations.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CanaryAdvanceResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed

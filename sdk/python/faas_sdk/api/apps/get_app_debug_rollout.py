from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.debug_rollout_explanation import DebugRolloutExplanation
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    deployment_id: None | Unset | UUID = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_deployment_id: None | str | Unset
    if isinstance(deployment_id, Unset):
        json_deployment_id = UNSET
    elif isinstance(deployment_id, UUID):
        json_deployment_id = str(deployment_id)
    else:
        json_deployment_id = deployment_id
    params["deployment_id"] = json_deployment_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/debug/rollout".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DebugRolloutExplanation | Problem | None:
    if response.status_code == 200:
        response_200 = DebugRolloutExplanation.from_dict(response.json())

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
) -> Response[DebugRolloutExplanation | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: None | Unset | UUID = UNSET,
) -> Response[DebugRolloutExplanation | Problem]:
    """Explain why a release was rolled back or is held.

     Explains the app's newest canary rollout, or `deployment_id`, with
    request evidence: the ADR-1447 decision that rolled it back, holds
    it, or advanced it; the candidate's routes compared with the stable
    deployment over the decision window, worst first; example requests
    that show the problem; and the candidate's open regressions. The
    evidence is read from request telemetry at request time and is not
    stored with the decision. `state` is `none` when no recent deployment
    had a canary rollout. Plan-gated by `DebugTelemetryEnabled`; the
    window is clamped to `DebugTelemetryRetentionDays`.

    Args:
        slug (str):
        deployment_id (None | Unset | UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DebugRolloutExplanation | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: None | Unset | UUID = UNSET,
) -> DebugRolloutExplanation | Problem | None:
    """Explain why a release was rolled back or is held.

     Explains the app's newest canary rollout, or `deployment_id`, with
    request evidence: the ADR-1447 decision that rolled it back, holds
    it, or advanced it; the candidate's routes compared with the stable
    deployment over the decision window, worst first; example requests
    that show the problem; and the candidate's open regressions. The
    evidence is read from request telemetry at request time and is not
    stored with the decision. `state` is `none` when no recent deployment
    had a canary rollout. Plan-gated by `DebugTelemetryEnabled`; the
    window is clamped to `DebugTelemetryRetentionDays`.

    Args:
        slug (str):
        deployment_id (None | Unset | UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DebugRolloutExplanation | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        deployment_id=deployment_id,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: None | Unset | UUID = UNSET,
) -> Response[DebugRolloutExplanation | Problem]:
    """Explain why a release was rolled back or is held.

     Explains the app's newest canary rollout, or `deployment_id`, with
    request evidence: the ADR-1447 decision that rolled it back, holds
    it, or advanced it; the candidate's routes compared with the stable
    deployment over the decision window, worst first; example requests
    that show the problem; and the candidate's open regressions. The
    evidence is read from request telemetry at request time and is not
    stored with the decision. `state` is `none` when no recent deployment
    had a canary rollout. Plan-gated by `DebugTelemetryEnabled`; the
    window is clamped to `DebugTelemetryRetentionDays`.

    Args:
        slug (str):
        deployment_id (None | Unset | UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DebugRolloutExplanation | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: None | Unset | UUID = UNSET,
) -> DebugRolloutExplanation | Problem | None:
    """Explain why a release was rolled back or is held.

     Explains the app's newest canary rollout, or `deployment_id`, with
    request evidence: the ADR-1447 decision that rolled it back, holds
    it, or advanced it; the candidate's routes compared with the stable
    deployment over the decision window, worst first; example requests
    that show the problem; and the candidate's open regressions. The
    evidence is read from request telemetry at request time and is not
    stored with the decision. `state` is `none` when no recent deployment
    had a canary rollout. Plan-gated by `DebugTelemetryEnabled`; the
    window is clamped to `DebugTelemetryRetentionDays`.

    Args:
        slug (str):
        deployment_id (None | Unset | UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DebugRolloutExplanation | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            deployment_id=deployment_id,
        )
    ).parsed

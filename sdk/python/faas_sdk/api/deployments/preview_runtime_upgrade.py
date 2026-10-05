from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.runtime_upgrade_preview_response import RuntimeUpgradePreviewResponse
from ...types import UNSET, Response


def _get_kwargs(
    id: str,
    *,
    target: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["target"] = target

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deployments/{id}/runtime/upgrade-preview".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RuntimeUpgradePreviewResponse | None:
    if response.status_code == 200:
        response_200 = RuntimeUpgradePreviewResponse.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RuntimeUpgradePreviewResponse]:
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
    target: str,
) -> Response[Problem | RuntimeUpgradePreviewResponse]:
    """Preview a runtime base change without applying it.

     Compares exact published component identities (ADR-596). This planning-only
    response cannot authorize an update. Runtime family changes, architecture changes
    and unknown current provenance block the plan. Same-family changes still require
    native qualification, a rebuilt candidate, fresh readiness and guarded rollout.
    Execution is unavailable; publication order does not prove a newer interpreter
    patch or a compatible update. Existing health history is advisory evidence.

    Args:
        id (str):
        target (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RuntimeUpgradePreviewResponse]
    """

    kwargs = _get_kwargs(
        id=id,
        target=target,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    target: str,
) -> Problem | RuntimeUpgradePreviewResponse | None:
    """Preview a runtime base change without applying it.

     Compares exact published component identities (ADR-596). This planning-only
    response cannot authorize an update. Runtime family changes, architecture changes
    and unknown current provenance block the plan. Same-family changes still require
    native qualification, a rebuilt candidate, fresh readiness and guarded rollout.
    Execution is unavailable; publication order does not prove a newer interpreter
    patch or a compatible update. Existing health history is advisory evidence.

    Args:
        id (str):
        target (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RuntimeUpgradePreviewResponse
    """

    return sync_detailed(
        id=id,
        client=client,
        target=target,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    target: str,
) -> Response[Problem | RuntimeUpgradePreviewResponse]:
    """Preview a runtime base change without applying it.

     Compares exact published component identities (ADR-596). This planning-only
    response cannot authorize an update. Runtime family changes, architecture changes
    and unknown current provenance block the plan. Same-family changes still require
    native qualification, a rebuilt candidate, fresh readiness and guarded rollout.
    Execution is unavailable; publication order does not prove a newer interpreter
    patch or a compatible update. Existing health history is advisory evidence.

    Args:
        id (str):
        target (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RuntimeUpgradePreviewResponse]
    """

    kwargs = _get_kwargs(
        id=id,
        target=target,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    target: str,
) -> Problem | RuntimeUpgradePreviewResponse | None:
    """Preview a runtime base change without applying it.

     Compares exact published component identities (ADR-596). This planning-only
    response cannot authorize an update. Runtime family changes, architecture changes
    and unknown current provenance block the plan. Same-family changes still require
    native qualification, a rebuilt candidate, fresh readiness and guarded rollout.
    Execution is unavailable; publication order does not prove a newer interpreter
    patch or a compatible update. Existing health history is advisory evidence.

    Args:
        id (str):
        target (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RuntimeUpgradePreviewResponse
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            target=target,
        )
    ).parsed

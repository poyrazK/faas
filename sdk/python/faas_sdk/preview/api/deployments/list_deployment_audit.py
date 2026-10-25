from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_deployment_audit_response import ListDeploymentAuditResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    limit: int | Unset = 50,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deployments/{id}/audit".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListDeploymentAuditResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListDeploymentAuditResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[ListDeploymentAuditResponse | Problem]:
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
    limit: int | Unset = 50,
) -> Response[ListDeploymentAuditResponse | Problem]:
    r"""List deployment audit timeline.

     Returns the deployment_audit rows for one deployment in
    reverse-chronological order (issue #976 / ADR-122 /
    SAFE-RELEASES-E.2 + production-leveling Stream A).

    The wire surface is a paginated JSON list
    (ListDeploymentAuditResponse); for the SSE-streaming
    variant of the build log itself see
    `/v1/deployments/{id}/logs`.

    IDOR posture: the handler resolves the deployment ID
    via `pkg/state.DeploymentByID` + `pkg/state.AppByID`
    + account match BEFORE returning rows. A
    cross-account probe returns 404 (no
    account-existence leak).

    Limit defaults to 50, clamped to [1, 500]; the
    server-applied limit is echoed back in the response so
    a paging consumer can distinguish \"limit was clamped\"
    from \"no more rows\" (both yield Items of length <
    limit).

    Args:
        id (str):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListDeploymentAuditResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
) -> ListDeploymentAuditResponse | Problem | None:
    r"""List deployment audit timeline.

     Returns the deployment_audit rows for one deployment in
    reverse-chronological order (issue #976 / ADR-122 /
    SAFE-RELEASES-E.2 + production-leveling Stream A).

    The wire surface is a paginated JSON list
    (ListDeploymentAuditResponse); for the SSE-streaming
    variant of the build log itself see
    `/v1/deployments/{id}/logs`.

    IDOR posture: the handler resolves the deployment ID
    via `pkg/state.DeploymentByID` + `pkg/state.AppByID`
    + account match BEFORE returning rows. A
    cross-account probe returns 404 (no
    account-existence leak).

    Limit defaults to 50, clamped to [1, 500]; the
    server-applied limit is echoed back in the response so
    a paging consumer can distinguish \"limit was clamped\"
    from \"no more rows\" (both yield Items of length <
    limit).

    Args:
        id (str):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListDeploymentAuditResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
) -> Response[ListDeploymentAuditResponse | Problem]:
    r"""List deployment audit timeline.

     Returns the deployment_audit rows for one deployment in
    reverse-chronological order (issue #976 / ADR-122 /
    SAFE-RELEASES-E.2 + production-leveling Stream A).

    The wire surface is a paginated JSON list
    (ListDeploymentAuditResponse); for the SSE-streaming
    variant of the build log itself see
    `/v1/deployments/{id}/logs`.

    IDOR posture: the handler resolves the deployment ID
    via `pkg/state.DeploymentByID` + `pkg/state.AppByID`
    + account match BEFORE returning rows. A
    cross-account probe returns 404 (no
    account-existence leak).

    Limit defaults to 50, clamped to [1, 500]; the
    server-applied limit is echoed back in the response so
    a paging consumer can distinguish \"limit was clamped\"
    from \"no more rows\" (both yield Items of length <
    limit).

    Args:
        id (str):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListDeploymentAuditResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
) -> ListDeploymentAuditResponse | Problem | None:
    r"""List deployment audit timeline.

     Returns the deployment_audit rows for one deployment in
    reverse-chronological order (issue #976 / ADR-122 /
    SAFE-RELEASES-E.2 + production-leveling Stream A).

    The wire surface is a paginated JSON list
    (ListDeploymentAuditResponse); for the SSE-streaming
    variant of the build log itself see
    `/v1/deployments/{id}/logs`.

    IDOR posture: the handler resolves the deployment ID
    via `pkg/state.DeploymentByID` + `pkg/state.AppByID`
    + account match BEFORE returning rows. A
    cross-account probe returns 404 (no
    account-existence leak).

    Limit defaults to 50, clamped to [1, 500]; the
    server-applied limit is echoed back in the response so
    a paging consumer can distinguish \"limit was clamped\"
    from \"no more rows\" (both yield Items of length <
    limit).

    Args:
        id (str):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListDeploymentAuditResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            limit=limit,
        )
    ).parsed

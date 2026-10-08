from http import HTTPStatus
from typing import Any
from urllib.parse import quote
import httpx
from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_action_preview_request import OperationWorkflowActionPreviewRequest
from ...models.operation_workflow_action_preview_response import OperationWorkflowActionPreviewResponse
from ...models.problem import Problem
from ...types import Response

def _get_kwargs(*,body:OperationWorkflowActionPreviewRequest)->dict[str,Any]:
    return {"method":"post","url":"/v1/platform-tenant-self/workflow-actions/preview","json":body.to_dict(),"headers":{"Content-Type":"application/json"}}

def _parse_response(*,client:AuthenticatedClient|Client,response:httpx.Response)->OperationWorkflowActionPreviewResponse|Problem|None:
    if response.status_code==200: return OperationWorkflowActionPreviewResponse.from_dict(response.json())
    if response.status_code in (400,401,403,404,409,429,503): return Problem.from_dict(response.json())
    if client.raise_on_unexpected_status: raise errors.UnexpectedStatus(response.status_code,response.content)
    return None

def _build_response(*,client:AuthenticatedClient|Client,response:httpx.Response)->Response[OperationWorkflowActionPreviewResponse|Problem]:
    return Response(status_code=HTTPStatus(response.status_code),content=response.content,headers=response.headers,parsed=_parse_response(client=client,response=response))

def sync_detailed(*,client:AuthenticatedClient|Client,body:OperationWorkflowActionPreviewRequest)->Response[OperationWorkflowActionPreviewResponse|Problem]:
    response=client.get_httpx_client().request(**_get_kwargs(body=body))
    return _build_response(client=client,response=response)

def sync(*,client:AuthenticatedClient|Client,body:OperationWorkflowActionPreviewRequest)->OperationWorkflowActionPreviewResponse|Problem|None:
    return sync_detailed(client=client,body=body).parsed

async def asyncio_detailed(*,client:AuthenticatedClient|Client,body:OperationWorkflowActionPreviewRequest)->Response[OperationWorkflowActionPreviewResponse|Problem]:
    response=await client.get_async_httpx_client().request(**_get_kwargs(body=body))
    return _build_response(client=client,response=response)

async def asyncio(*,client:AuthenticatedClient|Client,body:OperationWorkflowActionPreviewRequest)->OperationWorkflowActionPreviewResponse|Problem|None:
    return (await asyncio_detailed(client=client,body=body)).parsed

package mcpmanager

import (
	"context"
	"errors"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/mcpcompat"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerV2ContinuationProtocol(server *mcp.Server, service *continuationService) error {
	if err := mcp.AddReceivingCustomMethod[*mcpcompat.GetTaskParams, *mcpcompat.GetTaskResult](server, "tasks/get",
		func(ctx context.Context, _ *mcp.ServerSession, params *mcpcompat.GetTaskParams) (*mcpcompat.GetTaskResult, error) {
			if service == nil {
				return nil, errors.New("manager continuation service unavailable")
			}
			return service.GetTask(ctx, params.TaskID)
		}); err != nil {
		return err
	}
	if err := mcp.AddReceivingCustomMethod[*mcpcompat.UpdateTaskParams, *mcpcompat.UpdateTaskResult](server, "tasks/update",
		func(ctx context.Context, _ *mcp.ServerSession, params *mcpcompat.UpdateTaskParams) (*mcpcompat.UpdateTaskResult, error) {
			if service == nil {
				return nil, errors.New("manager continuation service unavailable")
			}
			return service.UpdateTask(ctx, params.TaskID, params.InputResponses)
		}); err != nil {
		return err
	}
	if err := mcp.AddReceivingCustomMethod[*mcpcompat.CancelTaskParams, *mcpcompat.CancelTaskResult](server, "tasks/cancel",
		func(ctx context.Context, _ *mcp.ServerSession, params *mcpcompat.CancelTaskParams) (*mcpcompat.CancelTaskResult, error) {
			if service == nil {
				return nil, errors.New("manager continuation service unavailable")
			}
			return service.CancelTask(ctx, params.TaskID)
		}); err != nil {
		return err
	}

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: managerResourceScheme + "://resource/{mapping_id}{?sig}",
		Name:        "Manager downstream continuation",
		Description: "Authenticated opaque continuation reference for a downstream resource link returned by a routed tool.",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		if service == nil || req == nil || req.Params == nil {
			return nil, mcp.ResourceNotFoundError("")
		}
		result, err := service.ReadResource(ctx, req.Params.URI)
		if errors.Is(err, ErrInvalidManagerResource) {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		return result, err
	})
	return nil
}

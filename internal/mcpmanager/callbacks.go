package mcpmanager

import (
	"context"
	"fmt"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const modernProtocolVersion = "2026-07-28"

type upstreamCallbackTarget struct {
	session *mcp.ServerSession
}

func newUpstreamCallbackTarget(session *mcp.ServerSession) downstream.LegacyCallbackTarget {
	if session == nil {
		return nil
	}
	return &upstreamCallbackTarget{session: session}
}

func (t *upstreamCallbackTarget) Elicit(ctx context.Context, request *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	params := t.initializeParams()
	if params == nil || params.ProtocolVersion >= modernProtocolVersion || params.Capabilities == nil || params.Capabilities.Elicitation == nil {
		return nil, fmt.Errorf("%w: upstream client does not support legacy elicitation", downstream.ErrLegacyCallbackUnsupported)
	}
	if request == nil || request.Params == nil {
		return nil, fmt.Errorf("%w: downstream elicitation request is missing params", downstream.ErrLegacyCallbackUnsupported)
	}
	return t.session.Elicit(ctx, request.Params)
}

func (t *upstreamCallbackTarget) CreateMessage(ctx context.Context, request *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	params := t.initializeParams()
	if params == nil || params.ProtocolVersion >= modernProtocolVersion || params.Capabilities == nil || params.Capabilities.Sampling == nil {
		return nil, fmt.Errorf("%w: upstream client does not support legacy sampling", downstream.ErrLegacyCallbackUnsupported)
	}
	if request == nil || request.Params == nil {
		return nil, fmt.Errorf("%w: downstream sampling request is missing params", downstream.ErrLegacyCallbackUnsupported)
	}
	return t.session.CreateMessage(ctx, request.Params)
}

func (t *upstreamCallbackTarget) ListRoots(ctx context.Context) (*mcp.ListRootsResult, error) {
	params := t.initializeParams()
	if params == nil || params.ProtocolVersion >= modernProtocolVersion || params.Capabilities == nil || params.Capabilities.RootsV2 == nil {
		return nil, fmt.Errorf("%w: upstream client does not support legacy roots", downstream.ErrLegacyCallbackUnsupported)
	}
	return t.session.ListRoots(ctx, nil)
}

func (t *upstreamCallbackTarget) initializeParams() *mcp.InitializeParams {
	if t == nil || t.session == nil {
		return nil
	}
	return t.session.InitializeParams()
}

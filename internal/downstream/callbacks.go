package downstream

import (
	"context"
	"errors"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var ErrLegacyCallbackUnsupported = errors.New("legacy_callback_unsupported")

// LegacyCallbackTarget is implemented by the Manager upstream boundary. A
// target is installed only for the lifetime of one routed tools/call, so a
// legacy downstream callback can be associated with exactly one upstream
// request instead of with process-global state.
type LegacyCallbackTarget interface {
	Elicit(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error)
	CreateMessage(context.Context, *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error)
	ListRoots(context.Context) (*mcp.ListRootsResult, error)
}

type legacyCallbackTargetKey struct{}

func WithLegacyCallbackTarget(ctx context.Context, target LegacyCallbackTarget) context.Context {
	if target == nil {
		return ctx
	}
	return context.WithValue(ctx, legacyCallbackTargetKey{}, target)
}

func legacyCallbackTargetFromContext(ctx context.Context) LegacyCallbackTarget {
	if ctx == nil {
		return nil
	}
	target, _ := ctx.Value(legacyCallbackTargetKey{}).(LegacyCallbackTarget)
	return target
}

type callbackState struct {
	mu     sync.RWMutex
	target LegacyCallbackTarget
}

func (s *callbackState) set(target LegacyCallbackTarget) {
	s.mu.Lock()
	s.target = target
	s.mu.Unlock()
}

func (s *callbackState) get() LegacyCallbackTarget {
	s.mu.RLock()
	target := s.target
	s.mu.RUnlock()
	return target
}

func installCallbackBridge(client *mcp.Client, opts *mcp.ClientOptions, state *callbackState) {
	previousElicit := opts.ElicitationHandler
	opts.ElicitationHandler = func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		if target := state.get(); target != nil {
			return target.Elicit(ctx, req)
		}
		if previousElicit != nil {
			return previousElicit(ctx, req)
		}
		return nil, ErrLegacyCallbackUnsupported
	}
	previousSample := opts.CreateMessageHandler
	opts.CreateMessageHandler = func(ctx context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
		if target := state.get(); target != nil {
			return target.CreateMessage(ctx, req)
		}
		if previousSample != nil {
			return previousSample(ctx, req)
		}
		return nil, ErrLegacyCallbackUnsupported
	}
	client.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "roots/list" {
				if target := state.get(); target != nil {
					return target.ListRoots(ctx)
				}
				return nil, ErrLegacyCallbackUnsupported
			}
			return next(ctx, method, req)
		}
	})
}

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routedlifecycle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectWithPersistentToolIdentity wraps downstream sessions so tools/list
// presence is not confused with semantic contract identity. The live subset is
// still exposed through CurrentTools, while InitialTools is the persistent
// semantic set used by indexing and execution-contract validation.
func connectWithPersistentToolIdentity(factory *downstream.Factory, c *catalog.Catalog, tracker *routingstate.Tracker) routedlifecycle.ConnectFunc {
	return func(ctx context.Context, entry v2config.ServerEntry) (routedlifecycle.RuntimeSession, error) {
		session, err := factory.Connect(ctx, entry)
		if err != nil {
			return nil, err
		}
		fail := func(cause error) (routedlifecycle.RuntimeSession, error) {
			_ = session.Close(context.Background())
			return nil, cause
		}

		live, err := filterExposedTools(session.InitialTools(), entry)
		if err != nil {
			return fail(err)
		}
		observationCtx := context.WithoutCancel(ctx)
		observation, err := c.ObserveServerTools(observationCtx, entry.ID, live.Tools)
		if err != nil {
			return fail(fmt.Errorf("observe downstream tools for %s: %w", entry.ID, err))
		}
		effective, err := cachedSemanticSnapshot(observationCtx, c, entry)
		if err != nil {
			return fail(err)
		}
		if observation.SemanticChanged {
			if err := c.MarkDirty(observationCtx, "server:"+entry.ID, "new or changed downstream tool contract", effective.Fingerprint); err != nil {
				return fail(fmt.Errorf("mark semantic tool change for %s: %w", entry.ID, err))
			}
			if _, err := tracker.AdvanceRoutingRevision(observationCtx); err != nil {
				return fail(fmt.Errorf("advance routing revision for %s tool change: %w", entry.ID, err))
			}
		}
		return &persistentToolSession{session: session, entry: entry, effective: effective}, nil
	}
}

type persistentToolSession struct {
	session   *downstream.Session
	entry     v2config.ServerEntry
	effective downstream.ToolSnapshot
}

func (s *persistentToolSession) InitialTools() downstream.ToolSnapshot {
	if s == nil {
		return downstream.ToolSnapshot{}
	}
	return s.effective.Clone()
}

func (s *persistentToolSession) CurrentTools() downstream.ToolSnapshot {
	if s == nil || s.session == nil {
		return downstream.ToolSnapshot{}
	}
	filtered, err := filterExposedTools(s.session.CurrentTools(), s.entry)
	if err != nil {
		return downstream.ToolSnapshot{}
	}
	return filtered
}

func (s *persistentToolSession) ToolContractChanged() bool {
	return s != nil && s.session != nil && s.session.ToolContractChanged()
}

func (s *persistentToolSession) Done() <-chan struct{} {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.Done()
}

func (s *persistentToolSession) Close(ctx context.Context) error {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.Close(ctx)
}

func (s *persistentToolSession) CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	if s == nil || s.session == nil {
		return nil, fmt.Errorf("%w: downstream session is unavailable", downstream.ErrDownstreamUnavailable)
	}
	if params != nil && params.Name != "" {
		live := s.CurrentTools()
		available := false
		for _, tool := range live.Tools {
			if tool != nil && tool.Name == params.Name {
				available = true
				break
			}
		}
		if !available {
			return nil, fmt.Errorf("%w: tool %s is temporarily unavailable on server %s", downstream.ErrDownstreamUnavailable, params.Name, s.entry.ID)
		}
	}
	result, err := s.session.CallTool(ctx, params)
	if errors.Is(err, downstream.ErrToolContractChanged) {
		// The lifecycle will reconnect this changed session before the next
		// acquire. That reconnect performs the authoritative per-tool comparison
		// and decides whether this was semantic drift or presence-only churn.
		return nil, fmt.Errorf("%w: downstream tool availability changed; reconnect required", downstream.ErrDownstreamUnavailable)
	}
	return result, err
}

func filterExposedTools(snapshot downstream.ToolSnapshot, entry v2config.ServerEntry) (downstream.ToolSnapshot, error) {
	tools := make([]*mcp.Tool, 0, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		if tool == nil || !entry.ToolExposed(tool.Name) {
			continue
		}
		tools = append(tools, tool)
	}
	fingerprint, canonical, err := toolcontract.FingerprintTools(tools)
	if err != nil {
		return downstream.ToolSnapshot{}, err
	}
	return downstream.ToolSnapshot{Tools: canonical, Fingerprint: fingerprint}, nil
}

func cachedSemanticSnapshot(ctx context.Context, c *catalog.Catalog, entry v2config.ServerEntry) (downstream.ToolSnapshot, error) {
	cached, err := c.CachedTools(ctx, entry.ID)
	if err != nil {
		return downstream.ToolSnapshot{}, err
	}
	tools := make([]*mcp.Tool, 0, len(cached))
	for _, record := range cached {
		if !entry.ToolExposed(record.ToolName) {
			continue
		}
		var tool mcp.Tool
		if err := json.Unmarshal(record.ContractJSON, &tool); err != nil {
			return downstream.ToolSnapshot{}, fmt.Errorf("decode cached tool %s/%s: %w", entry.ID, record.ToolName, err)
		}
		tools = append(tools, &tool)
	}
	fingerprint, canonical, err := toolcontract.FingerprintTools(tools)
	if err != nil {
		return downstream.ToolSnapshot{}, fmt.Errorf("fingerprint cached semantic tools for %s: %w", entry.ID, err)
	}
	return downstream.ToolSnapshot{Tools: canonical, Fingerprint: fingerprint}, nil
}

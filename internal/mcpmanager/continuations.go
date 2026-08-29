package mcpmanager

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/mcpcompat"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routedlifecycle"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const managerResourceScheme = "gtm-manager"

var (
	ErrInvalidManagerTask     = errors.New("invalid_manager_task")
	ErrInvalidManagerResource = errors.New("invalid_manager_resource")
)

type taskContinuationPayload struct {
	DownstreamTaskID string               `json:"downstream_task_id"`
	Status           mcpcompat.TaskStatus `json:"status"`
}

type resourceContinuationPayload struct {
	DownstreamURI string `json:"downstream_uri"`
}

type continuationService struct {
	catalog   *catalog.Catalog
	lifecycle *routedlifecycle.Service
	signing   []byte

	mu        sync.Mutex
	taskLease map[string]*routedlifecycle.UseLease
	timers    map[string]*time.Timer
	closed    bool
}

func newContinuationService(ctx context.Context, c *catalog.Catalog, lifecycle *routedlifecycle.Service, signingKey []byte) (*continuationService, error) {
	if c == nil {
		return nil, errors.New("catalog is required for continuation proxying")
	}
	if lifecycle == nil {
		return nil, errors.New("routed lifecycle is required for continuation proxying")
	}
	if len(signingKey) < 32 {
		return nil, errors.New("continuation signing key must contain at least 32 bytes")
	}
	s := &continuationService{
		catalog:   c,
		lifecycle: lifecycle,
		signing:   append([]byte(nil), signingKey...),
		taskLease: make(map[string]*routedlifecycle.UseLease),
		timers:    make(map[string]*time.Timer),
	}
	if _, err := c.DeleteExpiredContinuations(ctx, time.Now().UTC()); err != nil {
		return nil, err
	}
	// Re-establish task-held Managed Use Leases when possible. A temporarily
	// unavailable downstream does not destroy a durable mapping; the next task
	// operation retries acquisition without replaying the originating tool call.
	mappings, err := c.Continuations(ctx, catalog.ContinuationTask)
	if err != nil {
		return nil, err
	}
	for _, mapping := range mappings {
		payload, err := decodeTaskContinuation(mapping)
		if err != nil || taskTerminal(payload.Status) {
			_ = c.DeleteContinuation(ctx, mapping.ID)
			continue
		}
		ttl := continuationRemainingTTL(mapping)
		if ttl <= 0 {
			_ = c.DeleteContinuation(ctx, mapping.ID)
			continue
		}
		lease, acquireErr := lifecycle.AcquireTaskLease(ctx, mapping.ServerID, ttl)
		if acquireErr == nil {
			s.taskLease[mapping.ID] = lease
		}
		s.installExpiryTimer(mapping.ID, ttl)
	}
	return s, nil
}

func (s *continuationService) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	leases := make([]*routedlifecycle.UseLease, 0, len(s.taskLease))
	for _, lease := range s.taskLease {
		leases = append(leases, lease)
	}
	for _, timer := range s.timers {
		timer.Stop()
	}
	s.taskLease = make(map[string]*routedlifecycle.UseLease)
	s.timers = make(map[string]*time.Timer)
	s.mu.Unlock()
	for _, lease := range leases {
		lease.Release()
	}
}

func (s *continuationService) ProcessCallResult(ctx context.Context, serverID string, result *mcp.CallToolResult) (*mcp.CallToolResult, error) {
	if result == nil {
		return nil, nil
	}
	task, isTask, err := downstream.TaskFromCallResult(result)
	if err != nil {
		return nil, err
	}
	if isTask {
		return s.proxyCreatedTask(ctx, serverID, task)
	}
	return s.rewriteResourceLinks(ctx, serverID, result)
}

func (s *continuationService) proxyCreatedTask(ctx context.Context, serverID string, task *mcpcompat.CreateTaskResult) (*mcp.CallToolResult, error) {
	if task == nil || strings.TrimSpace(task.TaskID) == "" {
		return nil, ErrInvalidManagerTask
	}
	managerID, err := newOpaqueID("task_")
	if err != nil {
		return nil, err
	}
	ttl := taskTTL(task.Task.TTLMS)
	lease, err := s.lifecycle.AcquireTaskLease(ctx, serverID, ttl)
	if err != nil {
		return nil, fmt.Errorf("acquire task-held use lease: %w", err)
	}
	expires := lease.ExpiresAt()
	payload, err := json.Marshal(taskContinuationPayload{DownstreamTaskID: task.TaskID, Status: task.Status})
	if err != nil {
		lease.Release()
		return nil, err
	}
	mapping, _, err := s.catalog.PutContinuation(ctx, catalog.ContinuationMapping{
		ID:        managerID,
		Kind:      catalog.ContinuationTask,
		ServerID:  serverID,
		Payload:   payload,
		ExpiresAt: expires,
	})
	if err != nil {
		lease.Release()
		return nil, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		lease.Release()
		_ = s.catalog.DeleteContinuation(ctx, managerID)
		return nil, errors.New("continuation service is closed")
	}
	s.taskLease[managerID] = lease
	s.mu.Unlock()
	s.installExpiryTimer(managerID, continuationRemainingTTL(mapping))

	proxied := *task
	proxied.Task = task.Task
	proxied.Task.TaskID = managerID
	return downstream.CallResultForTask(&proxied)
}

func (s *continuationService) GetTask(ctx context.Context, managerID string) (*mcpcompat.GetTaskResult, error) {
	mapping, payload, err := s.resolveTask(ctx, managerID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureTaskLease(ctx, mapping); err != nil {
		return nil, err
	}
	lease, err := s.lifecycle.Acquire(ctx, mapping.ServerID)
	if err != nil {
		return nil, err
	}
	result, err := lease.GetTask(ctx, payload.DownstreamTaskID)
	lease.Release()
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("downstream tasks/get returned nil result")
	}
	result.TaskID = managerID
	if len(result.Result) != 0 {
		var final mcp.CallToolResult
		if json.Unmarshal(result.Result, &final) == nil {
			rewritten, rewriteErr := s.rewriteResourceLinks(ctx, mapping.ServerID, &final)
			if rewriteErr != nil {
				return nil, rewriteErr
			}
			if rewritten != nil {
				if body, marshalErr := json.Marshal(rewritten); marshalErr == nil {
					result.Result = body
				}
			}
		}
	}
	if taskTerminal(result.Status) {
		_ = s.finishTask(ctx, managerID)
	} else {
		_ = s.updateTaskStatus(ctx, mapping, payload, result.Status)
	}
	return result, nil
}

func (s *continuationService) UpdateTask(ctx context.Context, managerID string, responses mcp.InputResponseMap) (*mcpcompat.UpdateTaskResult, error) {
	mapping, payload, err := s.resolveTask(ctx, managerID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureTaskLease(ctx, mapping); err != nil {
		return nil, err
	}
	lease, err := s.lifecycle.Acquire(ctx, mapping.ServerID)
	if err != nil {
		return nil, err
	}
	result, err := lease.UpdateTask(ctx, payload.DownstreamTaskID, responses)
	lease.Release()
	return result, err
}

func (s *continuationService) CancelTask(ctx context.Context, managerID string) (*mcpcompat.CancelTaskResult, error) {
	mapping, payload, err := s.resolveTask(ctx, managerID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureTaskLease(ctx, mapping); err != nil {
		return nil, err
	}
	lease, err := s.lifecycle.Acquire(ctx, mapping.ServerID)
	if err != nil {
		return nil, err
	}
	result, err := lease.CancelTask(ctx, payload.DownstreamTaskID)
	if err == nil {
		if state, pollErr := lease.GetTask(ctx, payload.DownstreamTaskID); pollErr == nil && state != nil && taskTerminal(state.Status) {
			_ = s.finishTask(ctx, managerID)
		}
	}
	lease.Release()
	return result, err
}

func (s *continuationService) ReadResource(ctx context.Context, managerURI string) (*mcp.ReadResourceResult, error) {
	mappingID, err := s.validateResourceURI(managerURI)
	if err != nil {
		return nil, err
	}
	mapping, err := s.catalog.Continuation(ctx, mappingID)
	if err != nil || mapping.Kind != catalog.ContinuationResource {
		return nil, ErrInvalidManagerResource
	}
	var payload resourceContinuationPayload
	if err := json.Unmarshal(mapping.Payload, &payload); err != nil || strings.TrimSpace(payload.DownstreamURI) == "" {
		return nil, ErrInvalidManagerResource
	}
	lease, err := s.lifecycle.Acquire(ctx, mapping.ServerID)
	if err != nil {
		return nil, err
	}
	result, err := lease.ReadResource(ctx, payload.DownstreamURI)
	lease.Release()
	return result, err
}

func (s *continuationService) rewriteResourceLinks(ctx context.Context, serverID string, result *mcp.CallToolResult) (*mcp.CallToolResult, error) {
	if result == nil || len(result.Content) == 0 {
		return result, nil
	}
	copyResult := *result
	copyResult.Content = append([]mcp.Content(nil), result.Content...)
	changed := false
	for i, content := range copyResult.Content {
		link, ok := content.(*mcp.ResourceLink)
		if !ok {
			continue // EmbeddedResource is deliberately left byte/semantic-equivalent.
		}
		mappingID, err := newOpaqueID("res_")
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(resourceContinuationPayload{DownstreamURI: link.URI})
		if err != nil {
			return nil, err
		}
		if _, _, err := s.catalog.PutContinuation(ctx, catalog.ContinuationMapping{
			ID:       mappingID,
			Kind:     catalog.ContinuationResource,
			ServerID: serverID,
			Payload:  payload,
		}); err != nil {
			return nil, err
		}
		proxied := *link
		proxied.URI = s.resourceURI(mappingID)
		copyResult.Content[i] = &proxied
		changed = true
	}
	if !changed {
		return result, nil
	}
	return &copyResult, nil
}

func (s *continuationService) resolveTask(ctx context.Context, managerID string) (catalog.ContinuationMapping, taskContinuationPayload, error) {
	managerID = strings.TrimSpace(managerID)
	if !strings.HasPrefix(managerID, "task_") {
		return catalog.ContinuationMapping{}, taskContinuationPayload{}, ErrInvalidManagerTask
	}
	mapping, err := s.catalog.Continuation(ctx, managerID)
	if err != nil || mapping.Kind != catalog.ContinuationTask {
		return catalog.ContinuationMapping{}, taskContinuationPayload{}, ErrInvalidManagerTask
	}
	payload, err := decodeTaskContinuation(mapping)
	if err != nil || payload.DownstreamTaskID == "" || taskTerminal(payload.Status) {
		return catalog.ContinuationMapping{}, taskContinuationPayload{}, ErrInvalidManagerTask
	}
	return mapping, payload, nil
}

func (s *continuationService) ensureTaskLease(ctx context.Context, mapping catalog.ContinuationMapping) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("continuation service is closed")
	}
	if s.taskLease[mapping.ID] != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	ttl := continuationRemainingTTL(mapping)
	if ttl <= 0 {
		return ErrInvalidManagerTask
	}
	lease, err := s.lifecycle.AcquireTaskLease(ctx, mapping.ServerID, ttl)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if existing := s.taskLease[mapping.ID]; existing != nil {
		s.mu.Unlock()
		lease.Release()
		return nil
	}
	if s.closed {
		s.mu.Unlock()
		lease.Release()
		return errors.New("continuation service is closed")
	}
	s.taskLease[mapping.ID] = lease
	s.mu.Unlock()
	return nil
}

func (s *continuationService) updateTaskStatus(ctx context.Context, mapping catalog.ContinuationMapping, payload taskContinuationPayload, status mcpcompat.TaskStatus) error {
	if status == "" || status == payload.Status {
		return nil
	}
	payload.Status = status
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	// Mapping identity is immutable. Status is diagnostic only; preserving the
	// original mapping is sufficient for correctness, so avoid replacing it and
	// relying on mutable continuation rows.
	_ = body
	return nil
}

func (s *continuationService) finishTask(ctx context.Context, managerID string) error {
	s.mu.Lock()
	lease := s.taskLease[managerID]
	delete(s.taskLease, managerID)
	if timer := s.timers[managerID]; timer != nil {
		timer.Stop()
		delete(s.timers, managerID)
	}
	s.mu.Unlock()
	if lease != nil {
		lease.Release()
	}
	return s.catalog.DeleteContinuation(ctx, managerID)
}

func (s *continuationService) installExpiryTimer(managerID string, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if old := s.timers[managerID]; old != nil {
		old.Stop()
	}
	s.timers[managerID] = time.AfterFunc(ttl, func() {
		_ = s.finishTask(context.Background(), managerID)
	})
	s.mu.Unlock()
}

func (s *continuationService) resourceURI(mappingID string) string {
	signature := s.signResource(mappingID)
	return managerResourceScheme + "://resource/" + url.PathEscape(mappingID) + "?sig=" + url.QueryEscape(signature)
}

func (s *continuationService) validateResourceURI(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != managerResourceScheme || u.Host != "resource" {
		return "", ErrInvalidManagerResource
	}
	mappingID, err := url.PathUnescape(strings.TrimPrefix(u.EscapedPath(), "/"))
	if err != nil || mappingID == "" || strings.Contains(mappingID, "/") {
		return "", ErrInvalidManagerResource
	}
	provided := u.Query().Get("sig")
	expected := s.signResource(mappingID)
	if len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		return "", ErrInvalidManagerResource
	}
	return mappingID, nil
}

func (s *continuationService) signResource(mappingID string) string {
	mac := hmac.New(sha256.New, s.signing)
	_, _ = mac.Write([]byte("resource\x00" + mappingID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func decodeTaskContinuation(mapping catalog.ContinuationMapping) (taskContinuationPayload, error) {
	var payload taskContinuationPayload
	if err := json.Unmarshal(mapping.Payload, &payload); err != nil {
		return taskContinuationPayload{}, err
	}
	if strings.TrimSpace(payload.DownstreamTaskID) == "" {
		return taskContinuationPayload{}, errors.New("task continuation is missing downstream task id")
	}
	return payload, nil
}

func continuationRemainingTTL(mapping catalog.ContinuationMapping) time.Duration {
	if mapping.ExpiresAt == nil {
		return 24 * time.Hour
	}
	return time.Until(*mapping.ExpiresAt)
}

func taskTTL(ttlMS *int64) time.Duration {
	if ttlMS == nil || *ttlMS <= 0 {
		return 0 // Phase 9 maps non-positive TTL to its bounded maximum.
	}
	return time.Duration(*ttlMS) * time.Millisecond
}

func taskTerminal(status mcpcompat.TaskStatus) bool {
	switch status {
	case mcpcompat.TaskCompleted, mcpcompat.TaskFailed, mcpcompat.TaskCancelled:
		return true
	default:
		return false
	}
}

func newOpaqueID(prefix string) (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

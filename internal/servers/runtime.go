package servers

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/logging"
	proc "github.com/madesai98/GPT-Tunnel-Manager/internal/process"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/tunnelclient"
)

type Runtime interface {
	Done() <-chan struct{}
	Err() error
	Activity() <-chan time.Time
	ActivityTracking() bool
	Stop(context.Context) error
}

type RuntimeFactory interface {
	Start(context.Context, config.ServerEntry) (Runtime, error)
}

type tunnelRuntime interface {
	Runtime
}

type ownedProcess interface {
	Done() <-chan struct{}
	Err() error
	Stop(context.Context, time.Duration) error
}

type Factory struct {
	Installer            *tunnelclient.Installer
	Secrets              secrets.Store
	DefaultCredentialRef string
	HealthRoot           string
	Log                  *logging.Logger

	mu             sync.RWMutex
	BinaryOverride string
	Channel        string
}

func (f *Factory) SetTunnelClientConfig(binaryOverride, channel string) {
	f.mu.Lock()
	f.BinaryOverride = binaryOverride
	f.Channel = channel
	f.mu.Unlock()
}

func (f *Factory) tunnelClientConfig() (string, string) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.BinaryOverride, f.Channel
}

func (f *Factory) active(ctx context.Context) (tunnelclient.Active, error) {
	if f.Installer == nil {
		return tunnelclient.Active{}, errors.New("tunnel-client installer is unavailable")
	}
	binaryOverride, channel := f.tunnelClientConfig()
	return f.Installer.EnsureChannel(ctx, binaryOverride, channel)
}

func (f *Factory) Start(ctx context.Context, e config.ServerEntry) (runtime Runtime, err error) {
	// Server runtimes execute third-party MCP processes through tunnel-client.
	// Keep any unexpected panic in startup plumbing contained to this server so
	// one incompatible MCP cannot terminate GPT Tunnel Manager itself.
	defer func() {
		if recovered := recover(); recovered != nil {
			runtime = nil
			err = fmt.Errorf("server runtime startup panic: %v", recovered)
		}
	}()

	active, err := f.active(ctx)
	if err != nil {
		return nil, fmt.Errorf("ensure tunnel-client: %w", err)
	}
	ref := e.Tunnel.RuntimeCredentialRef
	if ref == "" {
		ref = f.DefaultCredentialRef
	}
	if ref == "" {
		return nil, errors.New("runtime credential reference is not configured")
	}
	key, err := f.Secrets.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("resolve runtime credential: %w", err)
	}
	if f.Log != nil {
		f.Log.Redactor().Register(key)
	}
	env := map[string]string{}
	for k, v := range e.Environment.Values {
		env[k] = v
	}
	for k, ref := range e.Environment.SecretRefs {
		v, err := f.Secrets.Get(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("resolve environment secret %s: %w", k, err)
		}
		if f.Log != nil {
			f.Log.Redactor().Register(v)
		}
		env[k] = string(v)
	}

	var owned *proc.Managed
	var mcpURL string
	var mcpCmd []string
	var tunnelDir string
	switch e.Transport.Type {
	case config.TransportStdio:
		mcpCmd = append([]string{e.Transport.Stdio.Executable}, e.Transport.Stdio.Args...)
		tunnelDir = e.Transport.Stdio.WorkingDirectory
	case config.TransportManagedHTTP:
		launch := e.Transport.ManagedHTTP.Launch
		owned, err = proc.Start(proc.Spec{
			Executable: launch.Executable,
			Args:       launch.Args,
			Dir:        launch.WorkingDirectory,
			Env:        mapEnv(env),
		}, f.logLine(e.ID, "Process"))
		if err != nil {
			return nil, err
		}
		mcpURL = e.Transport.ManagedHTTP.URL
	case config.TransportExternalHTTP:
		mcpURL = e.Transport.ExternalHTTP.URL
	default:
		return nil, fmt.Errorf("unsupported transport %q", e.Transport.Type)
	}

	spec := tunnelclient.RunSpec{
		Binary:              active.Path,
		TunnelID:            e.Tunnel.TunnelID,
		APIKey:              string(key),
		MCPURL:              mcpURL,
		MCPCommand:          mcpCmd,
		Dir:                 tunnelDir,
		Env:                 env,
		HealthDir:           f.HealthRoot,
		StartupTimeout:      e.StartupTimeout(),
		ShutdownTimeout:     e.ShutdownTimeout(),
		TelemetryCompatible: tunnelclient.TelemetryCompatible(active.Version),
		OnLog: func(stream, line string) {
			if f.Log != nil {
				f.Log.Log(logging.Info, e.ID, "Tunnel Client", line, map[string]any{"stream": stream})
			}
		},
	}
	tr, err := tunnelclient.Start(ctx, spec)
	if err != nil {
		if owned != nil {
			c, cancel := context.WithTimeout(context.Background(), e.ShutdownTimeout())
			_ = owned.Stop(c, e.ShutdownTimeout())
			cancel()
		}
		return nil, err
	}
	return newCombined(tr, owned, e.ShutdownTimeout()), nil
}

func (f *Factory) logLine(source, component string) func(proc.Line) {
	return func(line proc.Line) {
		if f.Log != nil {
			f.Log.Log(logging.Info, source, component, line.Text, map[string]any{"stream": line.Stream})
		}
	}
}

func mapEnv(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

type combined struct {
	tunnel     tunnelRuntime
	owned      ownedProcess
	shutdown   time.Duration
	done       chan struct{}
	finishOnce sync.Once
	mu         sync.RWMutex
	err        error
}

func newCombined(t tunnelRuntime, p ownedProcess, shutdown time.Duration) *combined {
	// A nil concrete pointer assigned to an interface is not equal to nil in Go.
	// Stdio entries have no separately-owned MCP process and previously passed a
	// nil *proc.Managed here, which looked non-nil and caused watch() to invoke a
	// method on the nil pointer. Normalize all nil-capable interface values at
	// this boundary so optional runtimes behave as genuinely absent.
	if nilOwnedProcess(p) {
		p = nil
	}
	c := &combined{tunnel: t, owned: p, shutdown: shutdown, done: make(chan struct{})}
	go c.watch()
	return c
}

func nilOwnedProcess(p ownedProcess) bool {
	if p == nil {
		return true
	}
	v := reflect.ValueOf(p)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func (c *combined) watch() {
	defer func() {
		if recovered := recover(); recovered != nil {
			c.finish(fmt.Errorf("server runtime watcher panic: %v", recovered))
		}
	}()

	if c.owned == nil {
		<-c.tunnel.Done()
		c.finish(c.tunnel.Err())
		return
	}

	var exitErr error
	var tunnelExited bool
	select {
	case <-c.tunnel.Done():
		tunnelExited = true
		exitErr = c.tunnel.Err()
	case <-c.owned.Done():
		exitErr = c.owned.Err()
	}

	// A Managed HTTP entry owns both processes. Do not report the combined
	// runtime as complete until the surviving partner has been terminated;
	// otherwise the supervisor can retry while an unreachable orphan remains.
	cleanupTimeout := c.shutdown + 2*time.Second
	if cleanupTimeout < 3*time.Second {
		cleanupTimeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	var cleanupErr error
	if tunnelExited {
		cleanupErr = c.owned.Stop(ctx, c.shutdown)
	} else {
		cleanupErr = c.tunnel.Stop(ctx)
	}
	if exitErr == nil && cleanupErr != nil {
		exitErr = cleanupErr
	}
	c.finish(exitErr)
}

func (c *combined) finish(err error) {
	c.finishOnce.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
	})
}

func (c *combined) Done() <-chan struct{} { return c.done }
func (c *combined) Err() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.err
}
func (c *combined) Activity() <-chan time.Time { return c.tunnel.Activity() }
func (c *combined) ActivityTracking() bool     { return c.tunnel.ActivityTracking() }

func (c *combined) Stop(ctx context.Context) error {
	var first error
	if err := c.tunnel.Stop(ctx); err != nil {
		first = err
	}
	if c.owned != nil {
		if err := c.owned.Stop(ctx, c.shutdown); err != nil && first == nil {
			first = err
		}
	}
	return first
}

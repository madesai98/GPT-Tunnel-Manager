package downstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	gtmprocess "github.com/madesai98/GPT-Tunnel-Manager/internal/process"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type stdioTransport struct {
	factory    *Factory
	serverID   string
	cmd        *exec.Cmd
	shutdown   time.Duration
	redactions []string

	mu      sync.RWMutex
	process *stdioProcess
}

func (f *Factory) newStdioTransport(ctx context.Context, server v2config.ServerEntry) (*stdioTransport, error) {
	cfg := server.Transport.Stdio
	if cfg == nil {
		return nil, errors.New("stdio transport configuration is missing")
	}
	env, redactions, err := f.resolveEnvironment(ctx, server)
	if err != nil {
		return nil, err
	}
	cmd := gtmprocess.ConfigureCommand(exec.Command(cfg.Executable, cfg.Args...))
	cmd.Dir = cfg.WorkingDirectory
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return &stdioTransport{
		factory:    f,
		serverID:   server.ID,
		cmd:        cmd,
		shutdown:   server.ShutdownTimeout(),
		redactions: redactions,
	}, nil
}

func (t *stdioTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open stdio MCP stdout: %w", err)
	}
	stdin, err := t.cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open stdio MCP stdin: %w", err)
	}
	stderr, err := t.cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("open stdio MCP stderr: %w", err)
	}
	if err := t.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start stdio MCP: %w", err)
	}

	proc := newStdioProcess(t.cmd, stdin, t.shutdown)
	t.mu.Lock()
	t.process = proc
	t.mu.Unlock()
	go scanLines(stderr, func(line string) {
		t.factory.emitLog(t.serverID, "stderr", line, t.redactions)
	})

	transport := &mcp.IOTransport{
		Reader: noCloseReader{ReadCloser: stdout},
		Writer: &stdioProtocolWriter{writer: stdin, process: proc},
	}
	connection, err := transport.Connect(ctx)
	if err != nil {
		_ = proc.shutdownProcess()
		return nil, err
	}
	return connection, nil
}

func (t *stdioTransport) Abort() error {
	t.mu.RLock()
	proc := t.process
	t.mu.RUnlock()
	if proc == nil {
		return nil
	}
	return proc.shutdownProcess()
}

func (t *stdioTransport) Done() <-chan struct{} {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.process == nil {
		return nil
	}
	return t.process.done
}

type noCloseReader struct{ io.ReadCloser }

func (noCloseReader) Close() error { return nil }

type stdioProtocolWriter struct {
	writer  io.WriteCloser
	process *stdioProcess
}

func (w *stdioProtocolWriter) Write(p []byte) (int, error) { return w.writer.Write(p) }
func (w *stdioProtocolWriter) Close() error                { return w.process.shutdownProcess() }

type stdioProcess struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	shutdown time.Duration
	done     chan struct{}

	mu      sync.RWMutex
	waitErr error
	once    sync.Once
	stopErr error
}

func newStdioProcess(cmd *exec.Cmd, stdin io.WriteCloser, shutdown time.Duration) *stdioProcess {
	if shutdown <= 0 {
		shutdown = 10 * time.Second
	}
	p := &stdioProcess{cmd: cmd, stdin: stdin, shutdown: shutdown, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		p.waitErr = err
		p.mu.Unlock()
		close(p.done)
	}()
	return p
}

func (p *stdioProcess) shutdownProcess() error {
	p.once.Do(func() {
		// Reserve some of the configured shutdown budget for the outer MCP
		// session close and pipe cleanup. The stages below remain within one
		// bounded process-shutdown budget instead of multiplying the timeout.
		budget := p.shutdown * 4 / 5
		if budget <= 0 {
			budget = p.shutdown
		}
		stdinWait := budget / 2
		gracefulWait := budget / 4
		forceWait := budget - stdinWait - gracefulWait

		// MCP stdio shutdown starts by closing protocol input. A child that exits
		// at this point has satisfied the requested ownership teardown even if it
		// chooses a non-zero process status for EOF. Unexpected exits are surfaced
		// before shutdown through Session.ensureAvailable and normal MCP errors.
		_ = p.stdin.Close()
		if _, ok := p.wait(stdinWait); ok {
			return
		}

		_ = gtmprocess.TerminateTreeGraceful(p.cmd)
		if _, ok := p.wait(gracefulWait); ok {
			return
		}

		_ = gtmprocess.TerminateTreeForce(p.cmd)
		if _, ok := p.wait(forceWait); ok {
			return
		}
		p.stopErr = errors.New("stdio MCP process tree did not exit after forced termination")
	})
	return p.stopErr
}

func (p *stdioProcess) wait(timeout time.Duration) (error, bool) {
	if timeout <= 0 {
		select {
		case <-p.done:
			p.mu.RLock()
			defer p.mu.RUnlock()
			return p.waitErr, true
		default:
			return nil, false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-p.done:
		p.mu.RLock()
		defer p.mu.RUnlock()
		return p.waitErr, true
	case <-timer.C:
		return nil, false
	}
}

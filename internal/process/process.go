package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

type Spec struct {
	Executable string
	Args       []string
	Dir        string
	Env        []string
}
type Line struct {
	Stream string
	Text   string
}
type Managed struct {
	mu       sync.RWMutex
	cmd      *exec.Cmd
	done     chan struct{}
	waitErr  error
	stopOnce sync.Once
}

// ConfigureCommand applies the platform-specific child process configuration used
// by Tunnel Manager. On Windows this prevents console windows from being created;
// on Unix it keeps the child in its own process group for lifecycle management.
func ConfigureCommand(cmd *exec.Cmd) *exec.Cmd {
	if cmd != nil {
		configure(cmd)
	}
	return cmd
}

func Start(spec Spec, onLine func(Line)) (*Managed, error) {
	if spec.Executable == "" {
		return nil, errors.New("process executable is required")
	}
	cmd := ConfigureCommand(exec.Command(spec.Executable, spec.Args...))
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	m := &Managed{cmd: cmd, done: make(chan struct{})}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", spec.Executable, err)
	}
	go scan(stdout, "stdout", onLine)
	go scan(stderr, "stderr", onLine)
	go func() {
		err := cmd.Wait()
		m.mu.Lock()
		m.waitErr = err
		m.mu.Unlock()
		close(m.done)
	}()
	return m, nil
}
func scan(r io.Reader, stream string, fn func(Line)) {
	if fn == nil {
		return
	}
	s := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	s.Buffer(buf, 1024*1024)
	for s.Scan() {
		fn(Line{Stream: stream, Text: s.Text()})
	}
}
func (m *Managed) PID() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cmd == nil || m.cmd.Process == nil {
		return 0
	}
	return m.cmd.Process.Pid
}
func (m *Managed) Done() <-chan struct{} { return m.done }
func (m *Managed) Err() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.waitErr
}
func (m *Managed) Stop(ctx context.Context, grace time.Duration) error {
	var stopErr error
	m.stopOnce.Do(func() {
		if grace <= 0 {
			grace = 10 * time.Second
		}
		if err := terminateGraceful(m.cmd); err != nil {
			stopErr = err
		}
		t := time.NewTimer(grace)
		defer t.Stop()
		select {
		case <-m.done:
			return
		case <-ctx.Done():
			stopErr = ctx.Err()
		case <-t.C:
		}
		_ = terminateForce(m.cmd)
		select {
		case <-m.done:
		case <-ctx.Done():
			if stopErr == nil {
				stopErr = ctx.Err()
			}
		}
	})
	return stopErr
}

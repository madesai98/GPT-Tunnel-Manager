package v2config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

type Store struct {
	Root         string
	AllocatePort func() (int, error)
	Now          func() time.Time

	freshInitBeforeCommit func(string) error
}

func NewStore(root string) *Store {
	return &Store{Root: root, AllocatePort: allocateLoopbackPort, Now: time.Now}
}

func (s *Store) ManagerPath() string { return filepath.Join(s.Root, "config", "manager.json") }
func (s *Store) ServersPath() string { return filepath.Join(s.Root, "config", "servers.json") }

func (s *Store) LoadOrCreate() (ManagerConfig, ServersConfig, error) {
	managerExists, err := fileExists(s.ManagerPath())
	if err != nil {
		return ManagerConfig{}, ServersConfig{}, fmt.Errorf("inspect manager config: %w", err)
	}
	serversExists, err := fileExists(s.ServersPath())
	if err != nil {
		return ManagerConfig{}, ServersConfig{}, fmt.Errorf("inspect servers config: %w", err)
	}

	if !managerExists && !serversExists {
		if _, err := os.Stat(filepath.Join(s.Root, "config")); err == nil {
			return ManagerConfig{}, ServersConfig{}, errors.New("incomplete v2 configuration: config directory exists without manager.json and servers.json")
		} else if !errors.Is(err, os.ErrNotExist) {
			return ManagerConfig{}, ServersConfig{}, fmt.Errorf("inspect config directory: %w", err)
		}
		return s.initializeFresh()
	}
	if managerExists != serversExists {
		return ManagerConfig{}, ServersConfig{}, errors.New("incomplete v2 configuration: manager.json and servers.json must both exist")
	}

	manager := ManagerConfig{}
	servers := ServersConfig{}
	if err := readJSON(s.ManagerPath(), &manager); err != nil {
		return ManagerConfig{}, ServersConfig{}, fmt.Errorf("load manager config: %w", err)
	}
	if err := readJSON(s.ServersPath(), &servers); err != nil {
		return ManagerConfig{}, ServersConfig{}, fmt.Errorf("load servers config: %w", err)
	}
	if err := ValidateManager(manager); err != nil {
		return manager, servers, fmt.Errorf("validate manager config: %w", err)
	}
	if err := ValidateServers(servers); err != nil {
		return manager, servers, fmt.Errorf("validate servers config: %w", err)
	}
	return manager, servers, nil
}

func (s *Store) initializeFresh() (ManagerConfig, ServersConfig, error) {
	allocator := s.AllocatePort
	if allocator == nil {
		allocator = allocateLoopbackPort
	}
	port, err := allocator()
	if err != nil {
		return ManagerConfig{}, ServersConfig{}, fmt.Errorf("allocate local Manager MCP port: %w", err)
	}
	manager := DefaultManagerConfig(port)
	servers := DefaultServersConfig()
	if err := ValidateManager(manager); err != nil {
		return ManagerConfig{}, ServersConfig{}, fmt.Errorf("validate fresh manager config: %w", err)
	}
	if err := ValidateServers(servers); err != nil {
		return ManagerConfig{}, ServersConfig{}, fmt.Errorf("validate fresh servers config: %w", err)
	}
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return ManagerConfig{}, ServersConfig{}, err
	}

	stage, err := os.MkdirTemp(s.Root, ".config-v2-init-*")
	if err != nil {
		return ManagerConfig{}, ServersConfig{}, fmt.Errorf("create fresh config staging directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := atomicWriteJSON(filepath.Join(stage, "manager.json"), manager); err != nil {
		return ManagerConfig{}, ServersConfig{}, fmt.Errorf("stage manager config: %w", err)
	}
	if err := atomicWriteJSON(filepath.Join(stage, "servers.json"), servers); err != nil {
		return ManagerConfig{}, ServersConfig{}, fmt.Errorf("stage servers config: %w", err)
	}
	if s.freshInitBeforeCommit != nil {
		if err := s.freshInitBeforeCommit(stage); err != nil {
			return ManagerConfig{}, ServersConfig{}, err
		}
	}
	if err := os.Rename(stage, filepath.Join(s.Root, "config")); err != nil {
		return ManagerConfig{}, ServersConfig{}, fmt.Errorf("commit fresh v2 configuration: %w", err)
	}
	committed = true
	return manager, servers, nil
}

func (s *Store) SaveManager(c ManagerConfig) error {
	if err := ValidateManager(c); err != nil {
		return err
	}
	return atomicWriteJSON(s.ManagerPath(), c)
}

func (s *Store) SaveServers(c ServersConfig) error {
	if err := ValidateServers(c); err != nil {
		return err
	}
	return atomicWriteJSON(s.ServersPath(), c)
}

// CutoverOpaqueLegacy moves the released v1 config and routing/runtime data
// directories aside without reading, decoding, or converting any file within
// them. tools/ is deliberately untouched because Manager Tunnel tooling remains
// part of v2.
func (s *Store) CutoverOpaqueLegacy() (string, error) {
	now := s.Now
	if now == nil {
		now = time.Now
	}
	legacyRoot, err := uniqueLegacyRoot(s.Root, now().UTC())
	if err != nil {
		return "", err
	}

	var existing []string
	for _, name := range []string{"config", "data"} {
		path := filepath.Join(s.Root, name)
		if _, err := os.Stat(path); err == nil {
			existing = append(existing, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect legacy %s directory: %w", name, err)
		}
	}
	if len(existing) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(legacyRoot, 0o700); err != nil {
		return "", fmt.Errorf("create opaque legacy directory: %w", err)
	}

	moved := make([]string, 0, len(existing))
	for _, name := range existing {
		from := filepath.Join(s.Root, name)
		to := filepath.Join(legacyRoot, name)
		if err := os.Rename(from, to); err != nil {
			for i := len(moved) - 1; i >= 0; i-- {
				_ = os.Rename(filepath.Join(legacyRoot, moved[i]), filepath.Join(s.Root, moved[i]))
			}
			_ = os.Remove(legacyRoot)
			return "", fmt.Errorf("move opaque legacy %s directory: %w", name, err)
		}
		moved = append(moved, name)
	}
	return legacyRoot, nil
}

func uniqueLegacyRoot(root string, now time.Time) (string, error) {
	base := filepath.Join(root, "legacy-v1-"+now.Format("20060102T150405Z"))
	for i := 0; i < 1000; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s-%03d", base, i)
		}
		_, err := os.Stat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("unable to allocate opaque legacy directory name")
}

func allocateLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || address.Port == 0 {
		return 0, errors.New("loopback listener did not return a TCP port")
	}
	return address.Port, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func readJSON(path string, dst any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func atomicWriteJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.json")
	if err != nil {
		return err
	}
	name := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temp.Write(body); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	ok = true
	return nil
}

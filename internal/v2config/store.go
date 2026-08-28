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
}

func NewStore(root string) *Store {
	return &Store{Root: root, AllocatePort: allocateLoopbackPort, Now: time.Now}
}

func (s *Store) ManagerPath() string { return filepath.Join(s.Root, "config", "manager.json") }
func (s *Store) ServersPath() string { return filepath.Join(s.Root, "config", "servers.json") }

func (s *Store) LoadOrCreate() (ManagerConfig, ServersConfig, error) {
	if err := os.MkdirAll(filepath.Join(s.Root, "config"), 0o700); err != nil {
		return ManagerConfig{}, ServersConfig{}, err
	}

	manager := ManagerConfig{}
	servers := DefaultServersConfig()
	createManager := false
	createServers := false

	if err := readJSON(s.ManagerPath(), &manager); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return ManagerConfig{}, ServersConfig{}, fmt.Errorf("load manager config: %w", err)
		}
		createManager = true
		allocator := s.AllocatePort
		if allocator == nil {
			allocator = allocateLoopbackPort
		}
		port, err := allocator()
		if err != nil {
			return ManagerConfig{}, ServersConfig{}, fmt.Errorf("allocate local Manager MCP port: %w", err)
		}
		manager = DefaultManagerConfig(port)
	}

	if err := readJSON(s.ServersPath(), &servers); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return manager, ServersConfig{}, fmt.Errorf("load servers config: %w", err)
		}
		createServers = true
		servers = DefaultServersConfig()
	}

	if err := ValidateManager(manager); err != nil {
		return manager, servers, fmt.Errorf("validate manager config: %w", err)
	}
	if err := ValidateServers(servers); err != nil {
		return manager, servers, fmt.Errorf("validate servers config: %w", err)
	}
	if createManager {
		if err := s.SaveManager(manager); err != nil {
			return manager, servers, err
		}
	}
	if createServers {
		if err := s.SaveServers(servers); err != nil {
			return manager, servers, err
		}
	}
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

package instance

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var ErrAlreadyRunning = errors.New("another GPT Tunnel Manager instance owns this portable root")

type lockRecord struct {
	RootHash string `json:"root_hash"`
	Addr     string `json:"addr"`
	Token    string `json:"token"`
}

type Owner struct {
	ln        net.Listener
	server    *http.Server
	mu        sync.RWMutex
	focus     func()
	rootHash  string
	token     string
	lockPath  string
	lockBytes []byte
}

func canonicalRoot(root string) string {
	value, err := filepath.Abs(root)
	if err != nil {
		value = filepath.Clean(root)
	}
	if resolved, err := filepath.EvalSymlinks(value); err == nil {
		value = resolved
	}
	value = filepath.Clean(value)
	if runtime.GOOS == "windows" {
		value = strings.ToLower(value)
	}
	return value
}

func hashRoot(root string) string {
	digest := sha256.Sum256([]byte(canonicalRoot(root)))
	return hex.EncodeToString(digest[:])
}

func randomToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func Acquire(root string) (*Owner, error) {
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dataDir, "instance.lock.json")
	rootHash := hashRoot(root)

	for attempt := 0; attempt < 8; attempt++ {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return becomeOwner(file, lockPath, rootHash)
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}

		existing, readErr := os.ReadFile(lockPath)
		if readErr == nil {
			var record lockRecord
			if json.Unmarshal(existing, &record) == nil && record.RootHash == rootHash && record.Addr != "" && record.Token != "" {
				if requestFocus(record) == nil {
					return nil, ErrAlreadyRunning
				}
			}
		}

		// A newly-created O_EXCL file may briefly exist before its owner has
		// finished writing the identity record and started serving HTTP. Never
		// delete a young lock: doing so could permit two owners for one root.
		if lockIsYoung(lockPath, 2*time.Second) {
			time.Sleep(50 * time.Millisecond)
			continue
		}

		// Only remove the exact stale lock we inspected. If another process
		// replaced it in the meantime, leave the newer owner's record intact.
		current, currentErr := os.ReadFile(lockPath)
		if readErr == nil && currentErr == nil && bytes.Equal(current, existing) {
			_ = os.Remove(lockPath)
			continue
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, err := os.Stat(lockPath); err == nil {
		return nil, ErrAlreadyRunning
	}
	return nil, errors.New("could not acquire the Portable Root instance lock")
}

func lockIsYoung(path string, age time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < age
}

func becomeOwner(file *os.File, lockPath, rootHash string) (*Owner, error) {
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(lockPath)
	}

	token, err := randomToken()
	if err != nil {
		cleanup()
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cleanup()
		return nil, err
	}

	record := lockRecord{RootHash: rootHash, Addr: listener.Addr().String(), Token: token}
	encoded, err := json.Marshal(record)
	if err != nil {
		listener.Close()
		cleanup()
		return nil, err
	}
	encoded = append(encoded, '\n')
	if _, err := file.Write(encoded); err != nil {
		listener.Close()
		cleanup()
		return nil, err
	}
	if err := file.Sync(); err != nil {
		listener.Close()
		cleanup()
		return nil, err
	}
	if err := file.Close(); err != nil {
		listener.Close()
		_ = os.Remove(lockPath)
		return nil, err
	}

	owner := &Owner{
		ln:        listener,
		rootHash:  rootHash,
		token:     token,
		lockPath:  lockPath,
		lockBytes: encoded,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/identity", owner.identity)
	mux.HandleFunc("/focus", owner.handleFocus)
	owner.server = &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = owner.server.Serve(listener) }()
	return owner, nil
}

func (o *Owner) identity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"root_hash": o.rootHash})
}

func (o *Owner) handleFocus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("X-GTM-Root") != o.rootHash || r.Header.Get("X-GTM-Token") != o.token {
		http.Error(w, "instance identity mismatch", http.StatusForbidden)
		return
	}
	o.mu.RLock()
	focus := o.focus
	o.mu.RUnlock()
	if focus != nil {
		focus()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (o *Owner) SetFocus(fn func()) {
	o.mu.Lock()
	o.focus = fn
	o.mu.Unlock()
}

func (o *Owner) Close(ctx context.Context) error {
	var shutdownErr error
	if o.server != nil {
		shutdownErr = o.server.Shutdown(ctx)
	}
	if current, err := os.ReadFile(o.lockPath); err == nil && bytes.Equal(current, o.lockBytes) {
		if err := os.Remove(o.lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}
	return shutdownErr
}

func requestFocus(record lockRecord) error {
	client := &http.Client{Timeout: 2 * time.Second}
	identityURL := "http://" + record.Addr + "/identity"
	response, err := client.Get(identityURL)
	if err != nil {
		return err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
	response.Body.Close()
	if readErr != nil {
		return readErr
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("identity HTTP %s", response.Status)
	}
	var identity struct {
		RootHash string `json:"root_hash"`
	}
	if json.Unmarshal(body, &identity) != nil || identity.RootHash != record.RootHash {
		return errors.New("Portable Root instance identity mismatch")
	}

	req, err := http.NewRequest(http.MethodPost, "http://"+record.Addr+"/focus", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-GTM-Root", record.RootHash)
	req.Header.Set("X-GTM-Token", record.Token)
	response, err = client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("focus HTTP %s", response.Status)
	}
	return nil
}

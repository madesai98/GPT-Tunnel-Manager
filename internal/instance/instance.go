package instance

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"net/http"
	"sync"
	"time"
)

var ErrAlreadyRunning = errors.New("another GPT Tunnel Manager instance owns this portable root")

type Owner struct {
	ln     net.Listener
	server *http.Server
	mu     sync.RWMutex
	focus  func()
}

func portFor(root string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(root))
	return 43000 + int(h.Sum32()%10000)
}

func Acquire(root string) (*Owner, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", portFor(root))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		_ = requestFocus(addr)
		return nil, ErrAlreadyRunning
	}

	owner := &Owner{ln: listener}
	mux := http.NewServeMux()
	mux.HandleFunc("/focus", func(w http.ResponseWriter, r *http.Request) {
		owner.mu.RLock()
		focus := owner.focus
		owner.mu.RUnlock()
		if focus != nil {
			focus()
		}
		w.WriteHeader(http.StatusNoContent)
	})
	owner.server = &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = owner.server.Serve(listener) }()
	return owner, nil
}

func (o *Owner) SetFocus(fn func()) {
	o.mu.Lock()
	o.focus = fn
	o.mu.Unlock()
}

func (o *Owner) Close(ctx context.Context) error {
	if o.server != nil {
		return o.server.Shutdown(ctx)
	}
	return nil
}

func requestFocus(addr string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Post("http://"+addr+"/focus", "application/json", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return nil
}

package admin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/logging"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/servers"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/tunnelclient"
)

//go:embed ui.html
var uiHTML string

const adminCookie = "gtm_admin"

type Backend interface {
	AdminState() any
	SaveManager(context.Context, config.ManagerConfig) error
	SaveServer(context.Context, config.ServerEntry) (config.ServerEntry, error)
	DeleteServer(context.Context, string) error
	Lifecycle(context.Context, string, string) (servers.Snapshot, error)
	PutSecret(context.Context, string, string) error
	DeleteSecret(context.Context, string) error
	Logs() []logging.Event
	ClearLogs()
	CheckUpdate(context.Context) (tunnelclient.Release, error)
	InstallUpdate(context.Context) (tunnelclient.Active, error)
	Rollback(context.Context) (tunnelclient.Active, error)
	RequestShutdown()
}

type Server struct {
	backend Backend
	http    *http.Server
	ln      net.Listener
	url     string
	token   string
}

func New(b Backend) *Server { return &Server{backend: b} }

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		_ = ln.Close()
		return fmt.Errorf("generate local admin token: %w", err)
	}
	s.token = base64.RawURLEncoding.EncodeToString(tokenBytes)
	s.ln = ln
	s.url = "http://" + ln.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.index)
	mux.HandleFunc("/api/state", s.state)
	mux.HandleFunc("/api/manager", s.manager)
	mux.HandleFunc("/api/server", s.serverEntry)
	mux.HandleFunc("/api/lifecycle/", s.lifecycle)
	mux.HandleFunc("/api/secret", s.secret)
	mux.HandleFunc("/api/logs", s.logs)
	mux.HandleFunc("/api/update/", s.update)
	mux.HandleFunc("/api/exit", s.exit)
	s.http = &http.Server{Handler: securityHeaders(mux), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.http.Serve(ln) }()
	return nil
}

func (s *Server) URL() string { return s.url }

func (s *Server) Stop(ctx context.Context) error {
	if s.http != nil {
		return s.http.Shutdown(ctx)
	}
	return nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookie,
		Value:    s.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(uiHTML))
}

func (s *Server) authorizeMutation(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.url {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return false
	}
	cookie, err := r.Cookie(adminCookie)
	if err != nil || len(cookie.Value) != len(s.token) || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(s.token)) != 1 {
		http.Error(w, "local admin session required", http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.backend.AdminState())
}

func (s *Server) manager(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeMutation(w, r) {
		return
	}
	var c config.ManagerConfig
	if err := decode(r, &c); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.backend.SaveManager(r.Context(), c); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) serverEntry(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		if !s.authorizeMutation(w, r) {
			return
		}
		var e config.ServerEntry
		if err := decode(r, &e); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		saved, err := s.backend.SaveServer(r.Context(), e)
		if err != nil {
			status := http.StatusBadRequest
			if err == servers.ErrRestartRequired {
				status = http.StatusConflict
			}
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if !s.authorizeMutation(w, r) {
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		if err := s.backend.DeleteServer(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) lifecycle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeMutation(w, r) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/lifecycle/"), "/")
	if len(parts) != 2 {
		http.Error(w, "expected /api/lifecycle/<id>/<action>", http.StatusBadRequest)
		return
	}
	snap, err := s.backend.Lifecycle(r.Context(), parts[0], parts[1])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, snap)
}

func (s *Server) secret(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeMutation(w, r) {
		return
	}
	var v struct {
		Ref   string `json:"ref"`
		Value string `json:"value"`
	}
	if err := decode(r, &v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var err error
	if r.Method == http.MethodPost {
		err = s.backend.PutSecret(r.Context(), v.Ref, v.Value)
	} else {
		err = s.backend.DeleteSecret(r.Context(), v.Ref)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		if !s.authorizeMutation(w, r) {
			return
		}
		s.backend.ClearLogs()
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	events := s.backend.Logs()
	switch r.URL.Query().Get("format") {
	case "jsonl":
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Disposition", `attachment; filename="gpt-tunnel-manager-logs.jsonl"`)
		for _, e := range events {
			b, _ := json.Marshal(e)
			_, _ = w.Write(append(b, '\n'))
		}
	case "text":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="gpt-tunnel-manager-logs.txt"`)
		for _, e := range events {
			_, _ = fmt.Fprintf(w, "%s %-5s %-18s %-14s %s\n", e.Timestamp.Format(time.RFC3339), strings.ToUpper(string(e.Level)), e.Source, e.Component, e.Message)
		}
	default:
		writeJSON(w, events)
	}
}

func (s *Server) update(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeMutation(w, r) {
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/update/")
	switch action {
	case "check":
		v, err := s.backend.CheckUpdate(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, v)
	case "install":
		v, err := s.backend.InstallUpdate(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, v)
	case "rollback":
		v, err := s.backend.Rollback(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, v)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) exit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeMutation(w, r) {
		return
	}
	s.backend.RequestShutdown()
	writeJSON(w, map[string]any{"ok": true})
}

func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 2<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

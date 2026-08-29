package downstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/platform"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

var (
	ErrOAuthConnectRequired   = errors.New("oauth_connect_required")
	ErrOAuthReconnectRequired = errors.New("oauth_reconnect_required")
)

const (
	oauthSessionSecretName     = "session"
	oauthInteractiveSecretName = "interactive"
)

type oauthSession struct {
	ClientID     string           `json:"client_id"`
	ClientSecret string           `json:"client_secret,omitempty"`
	AuthURL      string           `json:"auth_url"`
	TokenURL     string           `json:"token_url"`
	AuthStyle    oauth2.AuthStyle `json:"auth_style,omitempty"`
	RedirectURL  string           `json:"redirect_url"`
	Scopes       []string         `json:"scopes,omitempty"`
	Token        *oauth2.Token    `json:"token"`
}

type NativeOAuthProvider struct {
	secrets secrets.Store
	openURL func(context.Context, string) error
}

func NewNativeOAuthProvider(store secrets.Store) *NativeOAuthProvider {
	return &NativeOAuthProvider{secrets: store, openURL: platform.OpenURL}
}

func OAuthSessionSecretRef(serverID string) string {
	return OAuthSecretNamespace(serverID) + oauthSessionSecretName
}

func OAuthInteractiveSecretRef(serverID string) string {
	return OAuthSecretNamespace(serverID) + oauthInteractiveSecretName
}

func ArmOAuthConnect(ctx context.Context, store secrets.Store, serverID string) error {
	if store == nil {
		return errors.New("OAuth secret store is required")
	}
	if strings.TrimSpace(serverID) == "" {
		return errors.New("server id is required")
	}
	return store.Put(ctx, OAuthInteractiveSecretRef(serverID), []byte("1"))
}

func ClearOAuthSession(ctx context.Context, store secrets.Store, serverID string) error {
	if store == nil {
		return errors.New("OAuth secret store is required")
	}
	return errors.Join(
		deleteOAuthSecretIfPresent(ctx, store, OAuthSessionSecretRef(serverID)),
		deleteOAuthSecretIfPresent(ctx, store, OAuthInteractiveSecretRef(serverID)),
	)
}

func deleteOAuthSecretIfPresent(ctx context.Context, store secrets.Store, ref string) error {
	err := store.Delete(ctx, ref)
	if errors.Is(err, secrets.ErrNotFound) {
		return nil
	}
	return err
}

func OAuthConnected(ctx context.Context, store secrets.Store, serverID string) bool {
	if store == nil {
		return false
	}
	body, err := store.Get(ctx, OAuthSessionSecretRef(serverID))
	if err != nil {
		return false
	}
	var session oauthSession
	return json.Unmarshal(body, &session) == nil && session.Token != nil && session.ClientID != "" && session.Token.AccessToken != ""
}

func (p *NativeOAuthProvider) Handler(ctx context.Context, serverID string, cfg v2config.OAuthAuthConfig) (auth.OAuthHandler, error) {
	if p == nil || p.secrets == nil {
		return nil, errors.New("OAuth secret store is required")
	}
	session, hasSession, err := p.loadSession(ctx, serverID)
	if err != nil {
		return nil, err
	}
	armed := p.isArmed(ctx, serverID)
	if !hasSession && !armed {
		return nil, ErrOAuthConnectRequired
	}

	var listener net.Listener
	redirectURL := ""
	if armed {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("open OAuth callback listener: %w", err)
		}
		redirectURL = "http://" + listener.Addr().String() + "/oauth/callback"
		go func() {
			<-ctx.Done()
			_ = listener.Close()
		}()
	} else {
		redirectURL = session.RedirectURL
	}

	fetcher := p.authorizationFetcher(serverID, armed, listener)
	handlerCfg := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:              redirectURL,
		AuthorizationCodeFetcher: fetcher,
		RequestRefreshToken:      true,
	}
	if hasSession {
		credentials := &oauthex.ClientCredentials{ClientID: session.ClientID}
		if session.ClientSecret != "" {
			credentials.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: session.ClientSecret}
		}
		handlerCfg.PreregisteredClient = credentials
		oauthCfg := &oauth2.Config{
			ClientID: session.ClientID, ClientSecret: session.ClientSecret,
			Endpoint: oauth2.Endpoint{AuthURL: session.AuthURL, TokenURL: session.TokenURL, AuthStyle: session.AuthStyle},
			RedirectURL: session.RedirectURL, Scopes: append([]string(nil), session.Scopes...),
		}
		next := session
		handlerCfg.InitialTokenSource = &persistingOAuthTokenSource{
			base: oauthCfg.TokenSource(ctx, session.Token),
			save: func(refreshed *oauth2.Token) error {
				next.Token = refreshed
				return p.saveSession(context.Background(), serverID, next)
			},
		}
	} else {
		handlerCfg.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			RedirectURIs: []string{redirectURL},
			GrantTypes:   []string{"authorization_code", "refresh_token"},
			ResponseTypes: []string{"code"},
			ClientName:    "GPT Tunnel Manager",
			Scope:         strings.Join(cfg.Scopes, " "),
		}}
	}
	handlerCfg.NewTokenSource = func(tokenCtx context.Context, oauthCfg *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
		if oauthCfg == nil || token == nil {
			return nil, errors.New("OAuth exchange returned no token configuration")
		}
		next := oauthSession{
			ClientID: oauthCfg.ClientID, ClientSecret: oauthCfg.ClientSecret,
			AuthURL: oauthCfg.Endpoint.AuthURL, TokenURL: oauthCfg.Endpoint.TokenURL, AuthStyle: oauthCfg.Endpoint.AuthStyle,
			RedirectURL: oauthCfg.RedirectURL, Scopes: append([]string(nil), oauthCfg.Scopes...), Token: token,
		}
		if err := p.saveSession(tokenCtx, serverID, next); err != nil {
			return nil, err
		}
		_ = deleteOAuthSecretIfPresent(tokenCtx, p.secrets, OAuthInteractiveSecretRef(serverID))
		return &persistingOAuthTokenSource{
			base: oauthCfg.TokenSource(tokenCtx, token),
			save: func(refreshed *oauth2.Token) error {
				next.Token = refreshed
				return p.saveSession(context.Background(), serverID, next)
			},
		}, nil
	}

	handler, err := auth.NewAuthorizationCodeHandler(handlerCfg)
	if err != nil {
		if listener != nil {
			_ = listener.Close()
		}
		return nil, err
	}
	return handler, nil
}

func (p *NativeOAuthProvider) authorizationFetcher(serverID string, armed bool, listener net.Listener) auth.AuthorizationCodeFetcher {
	return func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		if !armed || listener == nil {
			return nil, ErrOAuthReconnectRequired
		}
		if args == nil || strings.TrimSpace(args.URL) == "" {
			return nil, errors.New("OAuth authorization URL is missing")
		}
		result := make(chan *auth.AuthorizationResult, 1)
		errCh := make(chan error, 1)
		server := &http.Server{ReadHeaderTimeout: 10 * time.Second}
		server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/oauth/callback" {
				http.NotFound(w, r)
				return
			}
			query := r.URL.Query()
			if oauthErr := query.Get("error"); oauthErr != "" {
				detail := query.Get("error_description")
				select {
				case errCh <- fmt.Errorf("OAuth authorization failed: %s %s", oauthErr, detail):
				default:
				}
				http.Error(w, "OAuth authorization failed. You can close this window.", http.StatusBadRequest)
				return
			}
			code := query.Get("code")
			state := query.Get("state")
			if code == "" || state == "" {
				select {
				case errCh <- errors.New("OAuth callback omitted code or state"):
				default:
				}
				http.Error(w, "OAuth callback was incomplete. You can close this window.", http.StatusBadRequest)
				return
			}
			select {
			case result <- &auth.AuthorizationResult{Code: code, State: state, Iss: query.Get("iss")}:
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = fmt.Fprintf(w, "<!doctype html><title>GPT Tunnel Manager</title><p>Connected %s. You can close this window.</p>", html.EscapeString(serverID))
			default:
			}
		})
		go func() {
			err := server.Serve(listener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				select {
				case errCh <- err:
				default:
				}
			}
		}()
		if err := p.openURL(ctx, args.URL); err != nil {
			_ = server.Close()
			return nil, fmt.Errorf("open OAuth authorization URL: %w", err)
		}
		defer server.Close()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-errCh:
			return nil, err
		case authorized := <-result:
			return authorized, nil
		}
	}
}

func (p *NativeOAuthProvider) isArmed(ctx context.Context, serverID string) bool {
	_, err := p.secrets.Get(ctx, OAuthInteractiveSecretRef(serverID))
	return err == nil
}

func (p *NativeOAuthProvider) loadSession(ctx context.Context, serverID string) (oauthSession, bool, error) {
	body, err := p.secrets.Get(ctx, OAuthSessionSecretRef(serverID))
	if errors.Is(err, secrets.ErrNotFound) {
		return oauthSession{}, false, nil
	}
	if err != nil {
		return oauthSession{}, false, err
	}
	var session oauthSession
	if err := json.Unmarshal(body, &session); err != nil {
		return oauthSession{}, false, fmt.Errorf("decode OAuth session for %s: %w", serverID, err)
	}
	if session.Token == nil || session.ClientID == "" || session.Token.AccessToken == "" {
		return oauthSession{}, false, nil
	}
	return session, true, nil
}

func (p *NativeOAuthProvider) saveSession(ctx context.Context, serverID string, session oauthSession) error {
	body, err := json.Marshal(session)
	if err != nil {
		return err
	}
	return p.secrets.Put(ctx, OAuthSessionSecretRef(serverID), body)
}

type persistingOAuthTokenSource struct {
	mu   sync.Mutex
	base oauth2.TokenSource
	save func(*oauth2.Token) error
	last string
}

func (s *persistingOAuthTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, err := s.base.Token()
	if err != nil {
		return nil, err
	}
	fingerprint := token.AccessToken + "\x00" + token.RefreshToken + "\x00" + token.Expiry.UTC().Format(time.RFC3339Nano)
	if fingerprint != s.last {
		if err := s.save(token); err != nil {
			return nil, err
		}
		s.last = fingerprint
	}
	return token, nil
}

func OAuthSessionIdentity(ctx context.Context, store secrets.Store, serverID string) ([]byte, error) {
	body, err := store.Get(ctx, OAuthSessionSecretRef(serverID))
	if err != nil {
		return nil, err
	}
	var session oauthSession
	if err := json.Unmarshal(body, &session); err != nil {
		return nil, err
	}
	identity := struct {
		ClientID  string           `json:"client_id"`
		AuthURL   string           `json:"auth_url"`
		TokenURL  string           `json:"token_url"`
		AuthStyle oauth2.AuthStyle `json:"auth_style,omitempty"`
		Scopes    []string         `json:"scopes,omitempty"`
	}{session.ClientID, session.AuthURL, session.TokenURL, session.AuthStyle, append([]string(nil), session.Scopes...)}
	return json.Marshal(identity)
}

func OAuthAuthorizationHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

package admin

import(
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/logging"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/servers"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/tunnelclient"
)
//go:embed ui.html
var uiHTML string
type Backend interface{AdminState()any;SaveManager(context.Context,config.ManagerConfig)error;SaveServer(context.Context,config.ServerEntry)(config.ServerEntry,error);DeleteServer(context.Context,string)error;Lifecycle(context.Context,string,string)(servers.Snapshot,error);PutSecret(context.Context,string,string)error;DeleteSecret(context.Context,string)error;Logs()[]logging.Event;ClearLogs();CheckUpdate(context.Context)(tunnelclient.Release,error);InstallUpdate(context.Context)(tunnelclient.Active,error);Rollback(context.Context)(tunnelclient.Active,error);RequestShutdown()}
type Server struct{backend Backend;http *http.Server;ln net.Listener;url string}
func New(b Backend)*Server{return &Server{backend:b}}
func(s *Server)Start()error{ln,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{return err};s.ln=ln;s.url="http://"+ln.Addr().String();mux:=http.NewServeMux();mux.HandleFunc("/",s.index);mux.HandleFunc("/api/state",s.state);mux.HandleFunc("/api/manager",s.manager);mux.HandleFunc("/api/server",s.serverEntry);mux.HandleFunc("/api/lifecycle/",s.lifecycle);mux.HandleFunc("/api/secret",s.secret);mux.HandleFunc("/api/logs",s.logs);mux.HandleFunc("/api/update/",s.update);mux.HandleFunc("/api/exit",s.exit);s.http=&http.Server{Handler:securityHeaders(mux),ReadHeaderTimeout:5*time.Second};go func(){_ = s.http.Serve(ln)}();return nil}
func(s *Server)URL()string{return s.url}
func(s *Server)Stop(ctx context.Context)error{if s.http!=nil{return s.http.Shutdown(ctx)};return nil}
func securityHeaders(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Content-Type-Options","nosniff");w.Header().Set("Referrer-Policy","no-referrer");w.Header().Set("Content-Security-Policy","default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'");next.ServeHTTP(w,r)})}
func(s *Server)index(w http.ResponseWriter,r *http.Request){if r.URL.Path!="/"{http.NotFound(w,r);return};w.Header().Set("Content-Type","text/html; charset=utf-8");_,_=w.Write([]byte(uiHTML))}
func(s *Server)state(w http.ResponseWriter,r *http.Request){if r.Method!=http.MethodGet{http.Error(w,"method not allowed",405);return};writeJSON(w,s.backend.AdminState())}
func(s *Server)manager(w http.ResponseWriter,r *http.Request){if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return};var c config.ManagerConfig;if err:=decode(r,&c);err!=nil{http.Error(w,err.Error(),400);return};if err:=s.backend.SaveManager(r.Context(),c);err!=nil{http.Error(w,err.Error(),400);return};writeJSON(w,map[string]any{"ok":true})}
func(s *Server)serverEntry(w http.ResponseWriter,r *http.Request){switch r.Method{case http.MethodPost:var e config.ServerEntry;if err:=decode(r,&e);err!=nil{http.Error(w,err.Error(),400);return};saved,err:=s.backend.SaveServer(r.Context(),e);if err!=nil{status:=400;if err==servers.ErrRestartRequired{status=409};http.Error(w,err.Error(),status);return};writeJSON(w,saved);case http.MethodDelete:id:=r.URL.Query().Get("id");if id==""{http.Error(w,"id required",400);return};if err:=s.backend.DeleteServer(r.Context(),id);err!=nil{http.Error(w,err.Error(),400);return};writeJSON(w,map[string]any{"ok":true});default:http.Error(w,"method not allowed",405)}}
func(s *Server)lifecycle(w http.ResponseWriter,r *http.Request){if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return};parts:=strings.Split(strings.TrimPrefix(r.URL.Path,"/api/lifecycle/"),"/");if len(parts)!=2{http.Error(w,"expected /api/lifecycle/<id>/<action>",400);return};snap,err:=s.backend.Lifecycle(r.Context(),parts[0],parts[1]);if err!=nil{http.Error(w,err.Error(),400);return};writeJSON(w,snap)}
func(s *Server)secret(w http.ResponseWriter,r *http.Request){var v struct{Ref string `json:"ref"`;Value string `json:"value"`};if err:=decode(r,&v);err!=nil{http.Error(w,err.Error(),400);return};var err error;switch r.Method{case http.MethodPost:err=s.backend.PutSecret(r.Context(),v.Ref,v.Value);case http.MethodDelete:err=s.backend.DeleteSecret(r.Context(),v.Ref);default:http.Error(w,"method not allowed",405);return};if err!=nil{http.Error(w,err.Error(),400);return};writeJSON(w,map[string]any{"ok":true})}
func(s *Server)logs(w http.ResponseWriter,r *http.Request){if r.Method==http.MethodDelete{s.backend.ClearLogs();writeJSON(w,map[string]any{"ok":true});return};if r.Method!=http.MethodGet{http.Error(w,"method not allowed",405);return};events:=s.backend.Logs();format:=r.URL.Query().Get("format");if format=="jsonl"{w.Header().Set("Content-Type","application/x-ndjson");for _,e:=range events{b,_:=json.Marshal(e);_,_=w.Write(append(b,'\n'))};return};if format=="text"{w.Header().Set("Content-Type","text/plain; charset=utf-8");for _,e:=range events{_,_=fmt.Fprintf(w,"%s %-5s %-18s %-14s %s\n",e.Timestamp.Format(time.RFC3339),strings.ToUpper(string(e.Level)),e.Source,e.Component,e.Message)};return};writeJSON(w,events)}
func(s *Server)update(w http.ResponseWriter,r *http.Request){if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return};action:=strings.TrimPrefix(r.URL.Path,"/api/update/");switch action{case"check":v,err:=s.backend.CheckUpdate(r.Context());if err!=nil{http.Error(w,err.Error(),502);return};writeJSON(w,v);case"install":v,err:=s.backend.InstallUpdate(r.Context());if err!=nil{http.Error(w,err.Error(),502);return};writeJSON(w,v);case"rollback":v,err:=s.backend.Rollback(r.Context());if err!=nil{http.Error(w,err.Error(),400);return};writeJSON(w,v);default:http.NotFound(w,r)}}
func(s *Server)exit(w http.ResponseWriter,r *http.Request){if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return};s.backend.RequestShutdown();writeJSON(w,map[string]any{"ok":true})}
func decode(r *http.Request,v any)error{r.Body=http.MaxBytesReader(nil,r.Body,2<<20);d:=json.NewDecoder(r.Body);d.DisallowUnknownFields();return d.Decode(v)}
func writeJSON(w http.ResponseWriter,v any){w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(v)}
func intQuery(r *http.Request,k string,def int)int{n,_:=strconv.Atoi(r.URL.Query().Get(k));if n==0{return def};return n}
var _=intQuery

package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var serverIDPattern=regexp.MustCompile(`^srv_[a-f0-9]{32}$`)
var tunnelIDPattern=regexp.MustCompile(`^tunnel_[a-f0-9]{32}$`)
var envNamePattern=regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func ValidateManager(c ManagerConfig) error {
	if c.SchemaVersion!=SchemaVersion { return fmt.Errorf("unsupported manager schema_version %d",c.SchemaVersion) }
	if c.ManagerTunnel.TunnelID!="" && !tunnelIDPattern.MatchString(c.ManagerTunnel.TunnelID) { return errors.New("manager_tunnel.tunnel_id must match tunnel_<32 lowercase hexadecimal chars>") }
	if c.ManagerTunnel.RuntimeCredentialRef!="" && !strings.HasPrefix(c.ManagerTunnel.RuntimeCredentialRef,"secret://") { return errors.New("manager_tunnel.runtime_credential_ref must use secret://") }
	if c.General.CloseBehavior!="minimize" && c.General.CloseBehavior!="exit" { return errors.New("general.close_behavior must be minimize or exit") }
	if c.ManagedDefaults.IdleTimeoutSeconds<0 { return errors.New("managed_defaults.idle_timeout_seconds cannot be negative") }
	switch c.Logging.MemoryLimitMB { case 5,10,25,50,100: default: return errors.New("logging.memory_limit_mb must be one of 5, 10, 25, 50, 100") }
	if c.Logging.MaximumFileSizeMB<=0 || c.Logging.KeepFiles<=0 { return errors.New("logging disk rotation limits must be positive") }
	if c.TunnelClient.UpdateCheckIntervalHours<=0 { return errors.New("tunnel_client.update_check_interval_hours must be positive") }
	switch c.Appearance.Theme { case "system","light","dark": default: return errors.New("appearance.theme must be system, light, or dark") }
	return nil
}
func ValidateServers(c ServersConfig) error { if c.SchemaVersion!=SchemaVersion { return fmt.Errorf("unsupported servers schema_version %d",c.SchemaVersion) }; seen:=map[string]bool{}; for i,e:=range c.Servers { if err:=ValidateServer(e); err!=nil { return fmt.Errorf("servers[%d]: %w",i,err) }; if seen[e.ID] { return fmt.Errorf("servers[%d]: duplicate server id %s",i,e.ID) }; seen[e.ID]=true }; return nil }
func ValidateServer(e ServerEntry) error {
	if !serverIDPattern.MatchString(e.ID) { return errors.New("id must match srv_<32 lowercase hex chars>") }
	if strings.TrimSpace(e.Name)=="" { return errors.New("name is required") }
	switch e.Mode { case ModeAlwaysOn,ModeManaged,ModeManual: default: return fmt.Errorf("invalid mode %q",e.Mode) }
	if !tunnelIDPattern.MatchString(e.Tunnel.TunnelID) { return errors.New("tunnel.tunnel_id must match tunnel_<32 lowercase hexadecimal chars>") }
	if e.Tunnel.RuntimeCredentialRef!="" && !strings.HasPrefix(e.Tunnel.RuntimeCredentialRef,"secret://") { return errors.New("tunnel.runtime_credential_ref must use secret://") }
	if e.Runtime.StartupTimeoutSeconds<0 || e.Runtime.ShutdownTimeoutSeconds<0 { return errors.New("runtime timeouts cannot be negative") }
	if e.Runtime.IdleTimeoutSeconds!=nil && *e.Runtime.IdleTimeoutSeconds<0 { return errors.New("runtime.idle_timeout_seconds cannot be negative") }
	for k:=range e.Environment.Values { if !envNamePattern.MatchString(k) { return fmt.Errorf("invalid environment name %q",k) } }
	for k,v:=range e.Environment.SecretRefs { if !envNamePattern.MatchString(k) { return fmt.Errorf("invalid environment secret name %q",k) }; if !strings.HasPrefix(v,"secret://") { return fmt.Errorf("environment secret %q must use secret://",k) } }
	return validateTransport(e.Transport)
}
func validateTransport(t TransportConfig) error { switch t.Type { case TransportStdio: if t.Stdio==nil || t.ManagedHTTP!=nil || t.ExternalHTTP!=nil { return errors.New("stdio transport must contain only stdio configuration") }; if strings.TrimSpace(t.Stdio.Executable)=="" { return errors.New("stdio.executable is required") }; case TransportManagedHTTP: if t.ManagedHTTP==nil || t.Stdio!=nil || t.ExternalHTTP!=nil { return errors.New("managed_http transport must contain only managed_http configuration") }; if err:=validateHTTPURL(t.ManagedHTTP.URL); err!=nil { return fmt.Errorf("managed_http.url: %w",err) }; if strings.TrimSpace(t.ManagedHTTP.Launch.Executable)=="" { return errors.New("managed_http.launch.executable is required") }; case TransportExternalHTTP: if t.ExternalHTTP==nil || t.Stdio!=nil || t.ManagedHTTP!=nil { return errors.New("external_http transport must contain only external_http configuration") }; if err:=validateHTTPURL(t.ExternalHTTP.URL); err!=nil { return fmt.Errorf("external_http.url: %w",err) }; default: return fmt.Errorf("invalid transport type %q",t.Type) }; return nil }
func validateHTTPURL(raw string) error { u,err:=url.Parse(raw); if err!=nil { return err }; if u.Scheme!="http" && u.Scheme!="https" { return errors.New("scheme must be http or https") }; if u.Host=="" { return errors.New("host is required") }; return nil }

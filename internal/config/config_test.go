package config
import "testing"
func TestNewServerIDValid(t *testing.T){id,err:=NewServerID();if err!=nil{t.Fatal(err)};if !serverIDPattern.MatchString(id){t.Fatalf("invalid id %q",id)}}
func TestDefaultManagerValid(t *testing.T){if err:=ValidateManager(DefaultManagerConfig());err!=nil{t.Fatal(err)}}
func TestRejectShellLikeMissingTransport(t *testing.T){e:=ServerEntry{ID:"srv_0123456789abcdef0123456789abcdef",Name:"x",Enabled:true,Mode:ModeManaged,Tunnel:TunnelConfig{TunnelID:"tunnel_0123456789abcdef0123456789abcdef"}};if err:=ValidateServer(e);err==nil{t.Fatal("expected transport validation error")}}

package mcpmanager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExactlyFourTools(t *testing.T) {
	xs := tools()
	if len(xs) != 4 { t.Fatalf("got %d tools",len(xs)) }
	want:=map[string]bool{"get_status":true,"start":true,"restart":true,"shutdown":true}
	for _,x:=range xs{name,_:=x["name"].(string);delete(want,name)}
	if len(want)!=0{t.Fatalf("missing %v",want)}
}

func TestRejectsBrowserOrigin(t *testing.T) {
	s := New(nil)
	req := httptest.NewRequest(http.MethodPost,"http://127.0.0.1/mcp",strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Origin","https://example.com")
	rec := httptest.NewRecorder()
	s.handle(rec,req)
	if rec.Code != http.StatusForbidden { t.Fatalf("got status %d",rec.Code) }
}

func TestStrictArgumentsRejectUnknownFields(t *testing.T) {
	var args struct{ServerID string `json:"server_id"`}
	if err:=decodeStrict([]byte(`{"server_id":"srv_0123456789abcdef0123456789abcdef","command":"calc.exe"}`),&args);err==nil{t.Fatal("expected unknown field rejection")}
}

package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMutationRequiresLocalAdminCookie(t *testing.T) {
	s:=&Server{token:"test-token",url:"http://127.0.0.1:43123"}
	req:=httptest.NewRequest(http.MethodPost,s.url+"/api/exit",nil)
	rec:=httptest.NewRecorder()
	if s.authorizeMutation(rec,req){t.Fatal("mutation without cookie was authorized")}
	if rec.Code!=http.StatusForbidden{t.Fatalf("got status %d",rec.Code)}
}

func TestMutationRejectsForeignOrigin(t *testing.T) {
	s:=&Server{token:"test-token",url:"http://127.0.0.1:43123"}
	req:=httptest.NewRequest(http.MethodPost,s.url+"/api/exit",nil)
	req.AddCookie(&http.Cookie{Name:adminCookie,Value:s.token})
	req.Header.Set("Origin","https://example.com")
	rec:=httptest.NewRecorder()
	if s.authorizeMutation(rec,req){t.Fatal("foreign origin was authorized")}
	if rec.Code!=http.StatusForbidden{t.Fatalf("got status %d",rec.Code)}
}

func TestMutationAcceptsSameOriginSession(t *testing.T) {
	s:=&Server{token:"test-token",url:"http://127.0.0.1:43123"}
	req:=httptest.NewRequest(http.MethodPost,s.url+"/api/exit",nil)
	req.AddCookie(&http.Cookie{Name:adminCookie,Value:s.token})
	req.Header.Set("Origin",s.url)
	rec:=httptest.NewRecorder()
	if !s.authorizeMutation(rec,req){t.Fatalf("same-origin local session rejected: %d",rec.Code)}
}

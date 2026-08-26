package instance

import(
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)
var ErrAlreadyRunning=errors.New("another GPT Tunnel Manager instance owns this portable root")
type Owner struct{ln net.Listener;server *http.Server;root string;mu sync.RWMutex;adminURL string;focus func()}
func portFor(root string)int{h:=fnv.New32a();_,_=h.Write([]byte(root));return 43000+int(h.Sum32()%10000)}
func Acquire(root string)(*Owner,error){addr:=fmt.Sprintf("127.0.0.1:%d",portFor(root));ln,err:=net.Listen("tcp",addr);if err!=nil{_ = requestFocus(root,addr);return nil,ErrAlreadyRunning};o:=&Owner{ln:ln,root:root};mux:=http.NewServeMux();mux.HandleFunc("/focus",func(w http.ResponseWriter,r *http.Request){o.mu.RLock();fn:=o.focus;url:=o.adminURL;o.mu.RUnlock();if fn!=nil{fn()};_ = json.NewEncoder(w).Encode(map[string]string{"admin_url":url})});o.server=&http.Server{Handler:mux,ReadHeaderTimeout:2*time.Second};go func(){_ = o.server.Serve(ln)}();return o,nil}
func(o *Owner)SetAdminURL(url string){o.mu.Lock();o.adminURL=url;o.mu.Unlock();dir:=filepath.Join(o.root,"data","instance");_ = os.MkdirAll(dir,0700);_ = os.WriteFile(filepath.Join(dir,"admin-url"),[]byte(url),0600)}
func(o *Owner)SetFocus(fn func()){o.mu.Lock();o.focus=fn;o.mu.Unlock()}
func(o *Owner)Close(ctx context.Context)error{_ = os.Remove(filepath.Join(o.root,"data","instance","admin-url"));if o.server!=nil{return o.server.Shutdown(ctx)};return nil}
func ExistingAdminURL(root string)string{b,_:=os.ReadFile(filepath.Join(root,"data","instance","admin-url"));return string(b)}
func requestFocus(root,addr string)error{c:=&http.Client{Timeout:2*time.Second};resp,err:=c.Post("http://"+addr+"/focus","application/json",nil);if err!=nil{return err};defer resp.Body.Close();return nil}

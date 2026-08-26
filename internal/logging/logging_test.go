package logging
import("strings";"testing")
func TestRedactionBeforeRing(t *testing.T){l,err:=New(t.TempDir(),"trace",5,false,"debug",1,1);if err!=nil{t.Fatal(err)};secret:=[]byte("sk-test-secret-value");l.Redactor().Register(secret);l.Log(Info,"Manager","Test","token sk-test-secret-value",map[string]any{"authorization":"Bearer abc","x":"sk-test-secret-value"});b:=l.Ring().Snapshot();if len(b)!=1{t.Fatal(len(b))};s:=b[0].Message+" "+b[0].Fields["x"].(string);if strings.Contains(s,string(secret)){t.Fatal("secret retained")}}

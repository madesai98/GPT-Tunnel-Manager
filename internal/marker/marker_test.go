package marker
import "testing"
func TestRoundTrip(t *testing.T){id:="srv_0123456789abcdef0123456789abcdef";got,err:=Parse(Generate(id));if err!=nil||got!=id{t.Fatalf("got %q err %v",got,err)}}

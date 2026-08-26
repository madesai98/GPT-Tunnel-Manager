package mcpmanager
import "testing"
func TestExactlyFourTools(t *testing.T){xs:=tools();if len(xs)!=4{t.Fatalf("got %d tools",len(xs))};want:=map[string]bool{"get_status":true,"start":true,"restart":true,"shutdown":true};for _,x:=range xs{name,_:=x["name"].(string);delete(want,name)};if len(want)!=0{t.Fatalf("missing %v",want)}}

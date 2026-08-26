package tunnelclient
import "testing"
func TestJoinCommand(t *testing.T){got:=JoinCommand([]string{"python","a b.py","--x=1"});if got!="python \"a b.py\" --x=1"{t.Fatalf("%q",got)}}
func TestMeaningfulActivity(t *testing.T){if !meaningfulActivity(`{"component":"dispatcher","rpc_method":"tools/call"}`){t.Fatal("tools/call should count")};if meaningfulActivity(`{"component":"dispatcher","rpc_method":"initialize"}`){t.Fatal("initialize should not count")}}

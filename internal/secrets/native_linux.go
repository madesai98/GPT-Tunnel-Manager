//go:build linux
package secrets
import("bytes";"context";"errors";"os/exec")
type nativeStore struct{}
func newNative(string)Store{return &nativeStore{}}
func(s *nativeStore)Put(ctx context.Context,ref string,v []byte)error{cmd:=exec.CommandContext(ctx,"secret-tool","store","--label=GPT Tunnel Manager","application","gpt-tunnel-manager","ref",ref);cmd.Stdin=bytes.NewReader(v);if out,err:=cmd.CombinedOutput();err!=nil{return errors.New("Linux Secret Service unavailable or locked: "+string(out))};return nil}
func(s *nativeStore)Get(ctx context.Context,ref string)([]byte,error){out,err:=exec.CommandContext(ctx,"secret-tool","lookup","application","gpt-tunnel-manager","ref",ref).Output();if err!=nil{return nil,ErrNotFound};return bytes.TrimSpace(out),nil}
func(s *nativeStore)Delete(ctx context.Context,ref string)error{_ = exec.CommandContext(ctx,"secret-tool","clear","application","gpt-tunnel-manager","ref",ref).Run();return nil}

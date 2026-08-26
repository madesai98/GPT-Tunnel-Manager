//go:build darwin
package secrets
import("context";"errors";"os/exec";"strings")
type nativeStore struct{}
func newNative(string)Store{return &nativeStore{}}
func(s *nativeStore)Put(ctx context.Context,ref string,v []byte)error{cmd:=exec.CommandContext(ctx,"security","add-generic-password","-U","-s","GPT-Tunnel-Manager","-a",ref,"-w",string(v));if out,err:=cmd.CombinedOutput();err!=nil{return errors.New(string(out))};return nil}
func(s *nativeStore)Get(ctx context.Context,ref string)([]byte,error){out,err:=exec.CommandContext(ctx,"security","find-generic-password","-s","GPT-Tunnel-Manager","-a",ref,"-w").Output();if err!=nil{return nil,ErrNotFound};return []byte(strings.TrimSpace(string(out))),nil}
func(s *nativeStore)Delete(ctx context.Context,ref string)error{_ = exec.CommandContext(ctx,"security","delete-generic-password","-s","GPT-Tunnel-Manager","-a",ref).Run();return nil}

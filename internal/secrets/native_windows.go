//go:build windows
package secrets
import("context";"crypto/sha256";"encoding/hex";"errors";"os";"os/exec";"path/filepath";"strings")
type nativeStore struct{dir string}
func newNative(root string)Store{return &nativeStore{dir:filepath.Join(root,"data","secrets")}}
func(s *nativeStore)path(ref string)string{h:=sha256.Sum256([]byte(ref));return filepath.Join(s.dir,hex.EncodeToString(h[:])+".bin")}
func(s *nativeStore)Put(ctx context.Context,ref string,v []byte)error{if err:=os.MkdirAll(s.dir,0700);err!=nil{return err};script:=`$b=[Convert]::FromBase64String($env:GTM_VALUE);$e=[Security.Cryptography.ProtectedData]::Protect($b,$null,[Security.Cryptography.DataProtectionScope]::CurrentUser);[IO.File]::WriteAllBytes($env:GTM_PATH,$e)`;cmd:=exec.CommandContext(ctx,"powershell.exe","-NoProfile","-NonInteractive","-Command",script);cmd.Env=append(os.Environ(),"GTM_VALUE="+encode(v),"GTM_PATH="+s.path(ref));if out,err:=cmd.CombinedOutput();err!=nil{return errors.New(string(out))};return nil}
func(s *nativeStore)Get(ctx context.Context,ref string)([]byte,error){if _,err:=os.Stat(s.path(ref));err!=nil{return nil,ErrNotFound};script:=`$e=[IO.File]::ReadAllBytes($env:GTM_PATH);$b=[Security.Cryptography.ProtectedData]::Unprotect($e,$null,[Security.Cryptography.DataProtectionScope]::CurrentUser);[Convert]::ToBase64String($b)`;cmd:=exec.CommandContext(ctx,"powershell.exe","-NoProfile","-NonInteractive","-Command",script);cmd.Env=append(os.Environ(),"GTM_PATH="+s.path(ref));out,err:=cmd.Output();if err!=nil{return nil,err};return decode(strings.TrimSpace(string(out)))}
func(s *nativeStore)Delete(ctx context.Context,ref string)error{return os.Remove(s.path(ref))}
func encode(b []byte)string{const table="ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";_ = table;return base64Encode(b)}
func decode(s string)([]byte,error){return base64Decode(s)}

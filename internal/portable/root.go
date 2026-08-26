package portable

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func Resolve(executable string)(string,error){ if executable=="" {p,err:=os.Executable();if err!=nil{return "",err};executable=p}; p,err:=filepath.Abs(executable);if err!=nil{return "",err};dir:=filepath.Dir(p);if runtime.GOOS=="darwin" {parts:=strings.Split(filepath.ToSlash(p),"/");for i:=len(parts)-1;i>=0;i--{if strings.HasSuffix(parts[i],".app"){bundle:=filepath.FromSlash(strings.Join(parts[:i+1],"/")); if !filepath.IsAbs(bundle){bundle=string(filepath.Separator)+bundle}; dir=filepath.Dir(bundle);break}}}; return dir,nil }
func EnsureWritable(root string)error{for _,d:=range []string{"config","data","tools"}{if err:=os.MkdirAll(filepath.Join(root,d),0700);err!=nil{return err}};f,err:=os.CreateTemp(filepath.Join(root,"data"),".write-test-");if err!=nil{return fmt.Errorf("portable root is not writable: %w",err)};name:=f.Name();_ = f.Close();_ = os.Remove(name);return nil}

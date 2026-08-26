//go:build windows
package process
import("fmt";"os/exec";"syscall")
func configure(cmd *exec.Cmd){cmd.SysProcAttr=&syscall.SysProcAttr{CreationFlags:0x00000200}}
func terminateGraceful(cmd *exec.Cmd)error{if cmd==nil||cmd.Process==nil{return nil};c:=exec.Command("taskkill","/PID",fmt.Sprint(cmd.Process.Pid),"/T");_ = c.Run();return nil}
func terminateForce(cmd *exec.Cmd)error{if cmd==nil||cmd.Process==nil{return nil};c:=exec.Command("taskkill","/PID",fmt.Sprint(cmd.Process.Pid),"/T","/F");_ = c.Run();return nil}

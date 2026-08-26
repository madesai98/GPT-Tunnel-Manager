//go:build !windows
package process
import("errors";"os/exec";"syscall")
func configure(cmd *exec.Cmd){cmd.SysProcAttr=&syscall.SysProcAttr{Setpgid:true}}
func terminateGraceful(cmd *exec.Cmd)error{if cmd==nil||cmd.Process==nil{return nil};err:=syscall.Kill(-cmd.Process.Pid,syscall.SIGTERM);if errors.Is(err,syscall.ESRCH){return nil};return err}
func terminateForce(cmd *exec.Cmd)error{if cmd==nil||cmd.Process==nil{return nil};err:=syscall.Kill(-cmd.Process.Pid,syscall.SIGKILL);if errors.Is(err,syscall.ESRCH){return nil};return err}

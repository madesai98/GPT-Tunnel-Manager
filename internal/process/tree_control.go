package process

import "os/exec"

// TerminateTreeGraceful asks a configured child process and the process tree
// owned with it to terminate without forcing an immediate kill. Commands should
// first be passed through ConfigureCommand so platform process-group ownership
// is established before Start.
func TerminateTreeGraceful(cmd *exec.Cmd) error {
	return terminateGraceful(cmd)
}

// TerminateTreeForce forcibly terminates a configured child process and the
// process tree owned with it.
func TerminateTreeForce(cmd *exec.Cmd) error {
	return terminateForce(cmd)
}

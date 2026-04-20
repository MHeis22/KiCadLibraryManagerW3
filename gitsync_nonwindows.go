//go:build !windows

package main

import "os/exec"

func gitCommand(args ...string) *exec.Cmd {
	return exec.Command("git", args...)
}

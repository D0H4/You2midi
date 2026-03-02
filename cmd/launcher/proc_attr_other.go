//go:build !windows

package main

import "os/exec"

func applyPlatformStartAttrs(_ *exec.Cmd) {}

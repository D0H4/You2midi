//go:build !windows

package main

func probeRegisteredInstallDirs() []string {
	return nil
}

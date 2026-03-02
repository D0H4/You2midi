//go:build windows

package main

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

const innoAppIDUninstallKey = `{8D85B931-35E5-4C37-A386-D2A9F13F021C}_is1`

func probeRegisteredInstallDirs() []string {
	paths := []string{
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\` + innoAppIDUninstallKey,
		`SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\` + innoAppIDUninstallKey,
	}

	results := make([]string, 0, 4)
	seen := map[string]struct{}{}
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, p := range paths {
			k, err := registry.OpenKey(root, p, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			for _, valueName := range []string{"Inno Setup: App Path", "InstallLocation"} {
				value, _, err := k.GetStringValue(valueName)
				if err != nil {
					continue
				}
				value = strings.TrimSpace(value)
				if value == "" {
					continue
				}
				if _, ok := seen[value]; ok {
					continue
				}
				seen[value] = struct{}{}
				results = append(results, value)
			}
			_ = k.Close()
		}
	}
	return results
}

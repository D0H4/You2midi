//go:build !windows

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func askYesNo(title string, message string) (bool, error) {
	_, _ = fmt.Fprintf(os.Stdout, "[%s] %s [y/N]: ", title, message)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func showInfo(title string, message string) error {
	_, err := fmt.Fprintf(os.Stdout, "[%s] %s\n", title, message)
	return err
}

func showError(title string, message string) error {
	_, err := fmt.Fprintf(os.Stderr, "[%s] %s\n", title, message)
	return err
}

func showWarning(title string, message string) error {
	_, err := fmt.Fprintf(os.Stdout, "[%s] %s\n", title, message)
	return err
}

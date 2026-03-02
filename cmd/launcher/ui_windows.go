//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

const (
	mbOK          = 0x00000000
	mbIconInfo    = 0x00000040
	mbIconWarning = 0x00000030
	mbIconError   = 0x00000010
	mbYesNo       = 0x00000004
	mbSystemModal = 0x00001000
	idYes         = 6
)

var (
	user32ProcMessageBoxW = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
)

func askYesNo(title string, message string) (bool, error) {
	ret, err := messageBox(title, message, mbYesNo|mbIconInfo|mbSystemModal)
	if err != nil {
		return false, err
	}
	return ret == idYes, nil
}

func showInfo(title string, message string) error {
	_, err := messageBox(title, message, mbOK|mbIconInfo|mbSystemModal)
	return err
}

func showError(title string, message string) error {
	_, err := messageBox(title, message, mbOK|mbIconError|mbSystemModal)
	return err
}

func showWarning(title string, message string) error {
	_, err := messageBox(title, message, mbOK|mbIconWarning|mbSystemModal)
	return err
}

func messageBox(title string, message string, flags uintptr) (uintptr, error) {
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return 0, err
	}
	messagePtr, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		return 0, err
	}
	ret, _, callErr := user32ProcMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(messagePtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		flags,
	)
	if ret == 0 && callErr != syscall.Errno(0) {
		return ret, callErr
	}
	return ret, nil
}

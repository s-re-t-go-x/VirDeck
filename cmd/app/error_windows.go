//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func showStartupError(message string) {
	fmt.Fprintln(os.Stderr, message)
	if len(os.Args) > 1 {
		return
	}
	text, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		return
	}
	title, _ := syscall.UTF16PtrFromString("VirDeck 启动失败")
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}

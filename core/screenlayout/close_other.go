//go:build !windows

package screenlayout

import "errors"

func Close(Window) error { return errors.New("窗口操作仅支持 Windows") }

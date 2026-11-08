//go:build !windows

package screenlayout

import "errors"

var errUnsupported = errors.New("native screen layout is only supported on Windows")

func Displays() ([]Display, error)               { return nil, errUnsupported }
func Windows() ([]Window, error)                 { return nil, errUnsupported }
func Place(window Window, bounds Rect) error     { return errUnsupported }
func Activate(window Window) error               { return errUnsupported }
func SetTitle(window Window, title string) error { return errUnsupported }

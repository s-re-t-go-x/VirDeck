//go:build !windows && !darwin

package screenlayout

func Close(Window) error { return ErrUnsupported }

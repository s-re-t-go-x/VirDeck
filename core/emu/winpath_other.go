//go:build !windows

package emu

func resolveWindowsASCIIPath(target string) (effective string, mode string, err error) {
	return target, PathCompatNone, nil
}

package emu

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CleanStaleLocks removes regular *.lock files inside <avdHome>/<avdName>.avd.
// Callers must verify via ADB/process inspection that the AVD is not currently running first.
func CleanStaleLocks(avdHome, avdName string) ([]string, error) {
	if strings.TrimSpace(avdHome) == "" || !isSafePathSegment(avdName) {
		return nil, fmt.Errorf("无效的 AVD 路径参数: %q", avdName)
	}
	avdDir := filepath.Join(avdHome, avdName+".avd")
	entries, err := os.ReadDir(avdDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var removed []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".lock") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		full := filepath.Join(avdDir, name)
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("清理锁文件 %s 失败: %w", name, err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}

// CleanStaleLocks removes stale *.lock files from this controller's AVD directory.
func (c *Controller) CleanStaleLocks(avdName string) ([]string, error) {
	return CleanStaleLocks(c.avdHome, avdName)
}

// ClearSnapshots removes the snapshots directory inside <avdHome>/<avdName>.avd.
func (c *Controller) ClearSnapshots(avdName string) error {
	if strings.TrimSpace(c.avdHome) == "" || !isSafePathSegment(avdName) {
		return fmt.Errorf("无效的 AVD 名称: %q", avdName)
	}
	snapDir := filepath.Join(c.avdHome, avdName+".avd", "snapshots")
	if err := os.RemoveAll(snapDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("清理快照目录失败: %w", err)
	}
	return nil
}

// IsGPUStartupFailure reports whether emulator output indicates a graphics/OpenGL/Vulkan initialization crash.
func IsGPUStartupFailure(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range []string{
		"opengl",
		"vulkan",
		"egl",
		"libgl",
		"swiftshader",
		"emugl",
		"gpu",
		"angle",
		"wgl",
		"failed to initialize graphics",
		"could not open opengl",
		"renderer",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// FallbackGPUMode returns the next graphics backend to try after an early GPU crash, or "" if exhausted.
func FallbackGPUMode(goos, goarch, currentMode string) string {
	cur := strings.ToLower(strings.TrimSpace(currentMode))
	if goos == "darwin" && goarch == "arm64" {
		switch cur {
		case "", "auto":
			return "host"
		case "host":
			return "swiftshader_indirect"
		default:
			return ""
		}
	}
	switch cur {
	case "", "auto", "host", "angle_indirect":
		return "swiftshader_indirect"
	default:
		return ""
	}
}

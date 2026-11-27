package emu

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// PathCompatNone indicates the original path is already ASCII (or non-Windows host).
	PathCompatNone = ""
	// PathCompatShortPath indicates Win32 8.3 short path transparently mapped a non-ASCII directory.
	PathCompatShortPath = "shortpath"
	// PathCompatJunction indicates a non-admin NTFS Junction transparently mapped a non-ASCII directory.
	PathCompatJunction = "junction"
	// PathCompatFailed indicates automatic Windows ASCII mapping could not be established.
	PathCompatFailed = "failed"

	dataDirPointerFile = "virdeck_datadir.txt"
)

// IsPureASCII reports whether s contains only 7-bit ASCII bytes.
func IsPureASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func sameDirectory(a, b string) bool {
	fiA, errA := os.Stat(a)
	fiB, errB := os.Stat(b)
	if errA != nil || errB != nil || !fiA.IsDir() || !fiB.IsDir() {
		return false
	}
	return os.SameFile(fiA, fiB)
}

func junctionHashName(target string) string {
	clean := strings.ToLower(filepath.Clean(target))
	sum := sha256.Sum256([]byte(clean))
	return "d_" + hex.EncodeToString(sum[:])[:12]
}

func userConfigPointerPath(userConfigDir string) string {
	if strings.TrimSpace(userConfigDir) == "" {
		return ""
	}
	return filepath.Join(userConfigDir, "VirDeck", "data_dir.txt")
}

func readDataDirOverride(exeBase, userConfigDir string) string {
	candidates := []string{
		filepath.Join(exeBase, dataDirPointerFile),
		userConfigPointerPath(userConfigDir),
	}
	for _, p := range candidates {
		if strings.TrimSpace(p) == "" {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func defaultPointerCandidates() []string {
	var out []string
	base := exeBaseDir()
	lowerBase := strings.ToLower(base)
	if !strings.Contains(lowerBase, "go-build") && !strings.Contains(lowerBase, "\\temp\\") && !strings.Contains(lowerBase, "/tmp/") {
		out = append(out, filepath.Join(base, dataDirPointerFile))
	}
	if cfgDir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(cfgDir) != "" {
		out = append(out, userConfigPointerPath(cfgDir))
	}
	return out
}

// SaveDataDirOverride validates newDir (must be writable, and pure ASCII on Windows),
// creates it if needed, and persists a startup pointer file so DefaultDataDir() uses it on next launch.
func SaveDataDirOverride(newDir string) (string, error) {
	return saveDataDirOverrideTo(newDir, runtime.GOOS, defaultPointerCandidates())
}

func saveDataDirOverrideTo(newDir, host string, pointerCandidates []string) (string, error) {
	raw := strings.TrimSpace(newDir)
	if raw == "" {
		return "", errors.New("数据目录路径不能为空")
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("解析路径失败: %w", err)
	}
	if host == "windows" && !IsPureASCII(abs) {
		return "", fmt.Errorf("目录 %q 仍包含中文或非 ASCII 字符，请选择纯英文路径", abs)
	}
	asciiOK, writableOK, warn := validateDataDirForHost(abs, host)
	if !asciiOK || !writableOK {
		if warn == "" {
			warn = "目标目录不可写或不符合要求"
		}
		return "", errors.New(warn)
	}
	var lastErr error
	for _, p := range pointerCandidates {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			lastErr = err
			continue
		}
		if err := os.WriteFile(p, []byte(abs+"\n"), 0o644); err != nil {
			lastErr = err
			continue
		}
		return p, nil
	}
	if lastErr != nil {
		return "", fmt.Errorf("写入数据目录配置失败: %w", lastErr)
	}
	return "", errors.New("未找到可写入的数据目录配置文件位置")
}

// SuggestPureASCIIDataDir returns a recommended writable pure-ASCII data directory on Windows,
// or the default ~/VirDeckData path on Unix.
func SuggestPureASCIIDataDir() string {
	home, _ := os.UserHomeDir()
	return suggestPureASCIIDataDirForHost(
		runtime.GOOS,
		home,
		os.Getenv("PUBLIC"),
		os.Getenv("ProgramData"),
		os.Getenv("LOCALAPPDATA"),
	)
}

func suggestPureASCIIDataDirForHost(host, home, publicDir, programData, localAppData string) string {
	if host != "windows" {
		if strings.TrimSpace(home) != "" {
			return filepath.Join(home, "VirDeckData")
		}
		return "VirDeckData"
	}
	candidates := []string{}
	if strings.TrimSpace(publicDir) != "" {
		candidates = append(candidates, filepath.Join(publicDir, "VirDeckData"))
	}
	if strings.TrimSpace(programData) != "" {
		candidates = append(candidates, filepath.Join(programData, "VirDeck", "VirDeckData"))
	}
	if strings.TrimSpace(localAppData) != "" {
		candidates = append(candidates, filepath.Join(localAppData, "VirDeckData"))
	}
	candidates = append(candidates, `C:\VirDeckData`)

	for _, cand := range candidates {
		if !IsPureASCII(cand) {
			continue
		}
		// Probe without permanently dirtying C:\ if nonexistent: check nearest existing parent or dir writability.
		if dirExists(cand) {
			if _, ok, _ := validateDataDirForHost(cand, "windows"); ok {
				return cand
			}
			continue
		}
		parent := filepath.Dir(cand)
		if dirExists(parent) {
			probe := filepath.Join(parent, ".virdeck_probe_tmp")
			if err := os.WriteFile(probe, []byte("ok"), 0o644); err == nil {
				_ = os.Remove(probe)
				return cand
			}
		}
	}
	return `C:\Users\Public\VirDeckData`
}

//go:build windows

package emu

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const (
	fileFlagBackupSemantics   = 0x02000000
	fileFlagOpenReparsePoint  = 0x00200000
	fsctlSetReparsePoint      = 0x000900A4
	ioReparseTagMountPoint    = 0xA0000003
)

var (
	modKernel32          = syscall.NewLazyDLL("kernel32.dll")
	procGetShortPathName = modKernel32.NewProc("GetShortPathNameW")
)

func getShortPathName(path string) (string, error) {
	src, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	n, _, callErr := procGetShortPathName.Call(uintptr(unsafe.Pointer(src)), 0, 0)
	if n == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return "", callErr
		}
		return "", errors.New("GetShortPathNameW returned 0")
	}
	buf := make([]uint16, n+1)
	n2, _, callErr2 := procGetShortPathName.Call(
		uintptr(unsafe.Pointer(src)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if n2 == 0 || int(n2) >= len(buf) {
		if callErr2 != nil && callErr2 != syscall.Errno(0) {
			return "", callErr2
		}
		return "", errors.New("GetShortPathNameW conversion failed")
	}
	return syscall.UTF16ToString(buf[:n2]), nil
}

// createNTFSJunction creates a standard-user NTFS directory junction (IO_REPARSE_TAG_MOUNT_POINT)
// at linkPath pointing to targetPath without requiring administrator privileges.
func createNTFSJunction(linkPath, targetPath string) error {
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absTarget, 0o755); err != nil {
		return err
	}

	if _, err := os.Lstat(linkPath); err == nil {
		if sameDirectory(linkPath, absTarget) {
			return nil
		}
		// Never use RemoveAll on a junction link: Remove deletes only the reparse entry.
		if rmErr := os.Remove(linkPath); rmErr != nil {
			return fmt.Errorf("清理旧目录联接失败: %w", rmErr)
		}
	}

	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(linkPath, 0o755); err != nil {
		return err
	}

	cleanupEmpty := true
	defer func() {
		if cleanupEmpty {
			_ = os.Remove(linkPath)
		}
	}()

	linkPtr, err := syscall.UTF16PtrFromString(linkPath)
	if err != nil {
		return err
	}
	handle, err := syscall.CreateFile(
		linkPtr,
		syscall.GENERIC_WRITE,
		0,
		nil,
		syscall.OPEN_EXISTING,
		fileFlagBackupSemantics|fileFlagOpenReparsePoint,
		0,
	)
	if err != nil {
		return fmt.Errorf("打开联接目录句柄失败: %w", err)
	}
	defer syscall.CloseHandle(handle)

	subName := `\??\` + absTarget
	printName := absTarget
	subU16 := utf16.Encode([]rune(subName))
	printU16 := utf16.Encode([]rune(printName))
	subByteLen := len(subU16) * 2
	printByteLen := len(printU16) * 2

	// Header layout:
	// [0..4]   ReparseTag (IO_REPARSE_TAG_MOUNT_POINT)
	// [4..6]   ReparseDataLength (8 + subByteLen + 2 + printByteLen + 2)
	// [6..8]   Reserved (0)
	// [8..10]  SubstituteNameOffset (0)
	// [10..12] SubstituteNameLength
	// [12..14] PrintNameOffset (subByteLen + 2)
	// [14..16] PrintNameLength
	// [16..]   PathBuffer: subU16 + NUL + printU16 + NUL
	reparseDataLen := 8 + subByteLen + 2 + printByteLen + 2
	if reparseDataLen > 16384-8 {
		return errors.New("NTFS 联接目标路径过长")
	}
	buf := make([]byte, 8+reparseDataLen)
	binary.LittleEndian.PutUint32(buf[0:4], ioReparseTagMountPoint)
	binary.LittleEndian.PutUint16(buf[4:6], uint16(reparseDataLen))
	binary.LittleEndian.PutUint16(buf[6:8], 0)
	binary.LittleEndian.PutUint16(buf[8:10], 0)
	binary.LittleEndian.PutUint16(buf[10:12], uint16(subByteLen))
	binary.LittleEndian.PutUint16(buf[12:14], uint16(subByteLen+2))
	binary.LittleEndian.PutUint16(buf[14:16], uint16(printByteLen))

	pos := 16
	for _, u := range subU16 {
		binary.LittleEndian.PutUint16(buf[pos:pos+2], u)
		pos += 2
	}
	pos += 2 // NUL
	for _, u := range printU16 {
		binary.LittleEndian.PutUint16(buf[pos:pos+2], u)
		pos += 2
	}

	var bytesReturned uint32
	err = syscall.DeviceIoControl(
		handle,
		fsctlSetReparsePoint,
		&buf[0],
		uint32(len(buf)),
		nil,
		0,
		&bytesReturned,
		nil,
	)
	if err != nil {
		return fmt.Errorf("设置 NTFS 目录联接失败: %w", err)
	}
	if !sameDirectory(linkPath, absTarget) {
		return errors.New("NTFS 目录联接验证失败")
	}
	cleanupEmpty = false
	return nil
}

func defaultJunctionBases() []string {
	raw := []string{
		filepath.Join(os.Getenv("PUBLIC"), "VirDeckLinks"),
		filepath.Join(os.Getenv("ProgramData"), "VirDeck", "links"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "VirDeckLinks"),
		filepath.Join(os.TempDir(), "VirDeckLinks"),
	}
	var out []string
	seen := map[string]bool{}
	for _, candidate := range raw {
		c := strings.TrimSpace(candidate)
		if c == "" || filepath.Dir(c) == c || !IsPureASCII(c) {
			continue
		}
		lower := strings.ToLower(c)
		if !seen[lower] {
			seen[lower] = true
			out = append(out, c)
		}
	}
	return out
}

func resolveWindowsASCIIPath(target string) (effective string, mode string, err error) {
	return resolveWindowsASCIIPathWithBases(target, defaultJunctionBases())
}

func resolveWindowsASCIIPathWithBases(target string, bases []string) (effective string, mode string, err error) {
	abs, absErr := filepath.Abs(strings.TrimSpace(target))
	if absErr == nil {
		target = abs
	}
	if IsPureASCII(target) {
		return target, PathCompatNone, nil
	}
	if mkErr := os.MkdirAll(target, 0o755); mkErr != nil {
		return target, PathCompatFailed, mkErr
	}

	// Tier 1: Win32 8.3 Short Path (zero extra directories created when volume keeps 8.3 enabled).
	if short, sErr := getShortPathName(target); sErr == nil && short != "" && IsPureASCII(short) && sameDirectory(short, target) {
		return short, PathCompatShortPath, nil
	}

	// Tier 2: Non-admin NTFS Directory Junction from an ASCII base.
	var lastErr error
	name := junctionHashName(target)
	for _, base := range bases {
		if !IsPureASCII(base) {
			continue
		}
		linkPath := filepath.Join(base, name)
		if jErr := createNTFSJunction(linkPath, target); jErr == nil && IsPureASCII(linkPath) {
			return linkPath, PathCompatJunction, nil
		} else {
			lastErr = jErr
		}
	}
	if lastErr == nil {
		lastErr = errors.New("未找到可建立纯英文 NTFS 目录联接的磁盘位置")
	}
	return target, PathCompatFailed, lastErr
}

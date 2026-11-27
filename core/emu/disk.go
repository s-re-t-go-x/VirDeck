package emu

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// AVDBackupMeta records portable instance settings saved alongside an AVD backup archive.
type AVDBackupMeta struct {
	Version      int    `json:"version"`
	InstanceName string `json:"instanceName"`
	SystemImage  string `json:"systemImage"`
	MemoryMB     int    `json:"memoryMB"`
	Cores        int    `json:"cores"`
	Resolution   string `json:"resolution"`
	StorageGB    int    `json:"storageGB"`
	QuickBoot    bool   `json:"quickBoot"`
	Proxy        string `json:"proxy,omitempty"`
	GPUMode      string `json:"gpuMode,omitempty"`
	Note         string `json:"note,omitempty"`
	Headless     bool   `json:"headless"`
	CreatedAt    string `json:"createdAt"`
}

// FindQemuImg locates the qemu-img binary inside sdkRoot or system PATH.
func FindQemuImg(sdkRoot string) string {
	exe := "qemu-img"
	if runtime.GOOS == "windows" {
		exe = "qemu-img.exe"
	}
	if strings.TrimSpace(sdkRoot) != "" {
		candidates := []string{
			filepath.Join(sdkRoot, "emulator", exe),
		}
		qemuBase := filepath.Join(sdkRoot, "emulator", "qemu")
		for _, sub := range []string{"windows-x86_64", "linux-x86_64", "darwin-aarch64", "darwin-x86_64"} {
			candidates = append(candidates, filepath.Join(qemuBase, sub, exe))
		}
		if entries, err := os.ReadDir(qemuBase); err == nil {
			for _, ent := range entries {
				if ent.IsDir() {
					candidates = append(candidates, filepath.Join(qemuBase, ent.Name(), exe))
				}
			}
		}
		for _, cand := range candidates {
			if st, err := os.Stat(cand); err == nil && !st.IsDir() {
				return cand
			}
		}
	}
	if found, err := exec.LookPath(exe); err == nil {
		return found
	}
	return ""
}

var findQemuImgFn = FindQemuImg

// CompactAVDDisk removes regenerable cache images and shrinks qcow2 overlays via qemu-img when available.
// It preserves backing-file references (-B) and never replaces a disk if compaction would increase size.
func CompactAVDDisk(ctx context.Context, avdHome, sdkRoot, avdName string) (beforeBytes, afterBytes int64, usedQemuImg bool, err error) {
	if strings.TrimSpace(avdHome) == "" || !isSafePathSegment(avdName) {
		return 0, 0, false, fmt.Errorf("无效的 AVD 目录配置: %q", avdName)
	}
	avdDir := filepath.Join(avdHome, avdName+".avd")
	if !dirExists(avdDir) {
		return 0, 0, false, fmt.Errorf("找不到实例数据目录: %s", avdDir)
	}

	targets := []string{"userdata-qemu.img.qcow2", "sdcard.img.qcow2", "cache.img.qcow2"}
	for _, name := range targets {
		if st, statErr := os.Stat(filepath.Join(avdDir, name)); statErr == nil && !st.IsDir() {
			beforeBytes += st.Size()
		}
	}

	// cache.img.qcow2 holds regenerable Android cache; removing it is safe when offline.
	_ = os.Remove(filepath.Join(avdDir, "cache.img.qcow2"))

	qemuImg := findQemuImgFn(sdkRoot)
	if qemuImg != "" {
		for _, name := range []string{"userdata-qemu.img.qcow2", "sdcard.img.qcow2"} {
			src := filepath.Join(avdDir, name)
			st, statErr := os.Stat(src)
			if statErr != nil || st.IsDir() || st.Size() == 0 {
				continue
			}
			ok, compactErr := compactOneQcow2(ctx, qemuImg, src, st.Size())
			if compactErr != nil {
				return beforeBytes, 0, usedQemuImg, compactErr
			}
			if ok {
				usedQemuImg = true
			}
		}
	}

	for _, name := range targets {
		if st, statErr := os.Stat(filepath.Join(avdDir, name)); statErr == nil && !st.IsDir() {
			afterBytes += st.Size()
		}
	}
	return beforeBytes, afterBytes, usedQemuImg, nil
}

func compactOneQcow2(ctx context.Context, qemuImg, src string, origSize int64) (bool, error) {
	backing, err := inspectQcow2BackingFile(ctx, qemuImg, src)
	if err != nil {
		return false, err
	}
	if backing != "" && !isSafeBackingPath(backing, true) {
		return false, fmt.Errorf("拒绝不安全的 qcow2 backing-filename: %q", backing)
	}
	tmp := src + ".compact.tmp"
	_ = os.Remove(tmp)

	args := []string{"convert", "-O", "qcow2"}
	if backing != "" {
		args = append(args, "-B", backing)
	}
	args = append(args, src, tmp)

	cmd := exec.CommandContext(ctx, qemuImg, args...)
	hideConsole(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp)
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return false, fmt.Errorf("压缩磁盘镜像 %s 失败: %w (%s)", filepath.Base(src), err, msg)
		}
		return false, fmt.Errorf("压缩磁盘镜像 %s 失败: %w", filepath.Base(src), err)
	}

	tmpStat, err := os.Stat(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	if tmpStat.Size() > 0 && tmpStat.Size() < origSize {
		if err := os.Rename(tmp, src); err != nil {
			_ = os.Remove(tmp)
			return false, err
		}
	} else {
		_ = os.Remove(tmp)
	}
	return true, nil
}

func inspectQcow2BackingFile(ctx context.Context, qemuImg, src string) (string, error) {
	cmd := exec.CommandContext(ctx, qemuImg, "info", "--output=json", src)
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("读取磁盘镜像信息 %s 失败: %w", filepath.Base(src), err)
	}
	var info struct {
		BackingFilename string `json:"backing-filename"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return "", nil
	}
	return strings.TrimSpace(info.BackingFilename), nil
}

// BackupAVD archives an offline AVD directory and metadata into destZip atomically.
func BackupAVD(avdHome, avdName, destZip string, meta AVDBackupMeta) (int64, error) {
	if strings.TrimSpace(avdHome) == "" || !isSafePathSegment(avdName) {
		return 0, fmt.Errorf("无效的 AVD 路径: %q", avdName)
	}
	if strings.TrimSpace(destZip) == "" {
		return 0, errors.New("备份文件路径不能为空")
	}
	avdDir := filepath.Join(avdHome, avdName+".avd")
	if !dirExists(avdDir) {
		return 0, fmt.Errorf("找不到实例数据目录: %s", avdDir)
	}
	if err := os.MkdirAll(filepath.Dir(destZip), 0o755); err != nil {
		return 0, err
	}
	if meta.Version <= 0 {
		meta.Version = 1
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(destZip), ".virdeck-backup-*.tmp")
	if err != nil {
		return 0, err
	}
	tmpZip := tmpFile.Name()
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpZip)
		return 0, err
	}
	zw := zip.NewWriter(tmpFile)
	writeErr := func() error {
		metaBytes, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			return err
		}
		if err := addZipBytes(zw, "virdeck-backup.json", metaBytes); err != nil {
			return err
		}
		iniPath := filepath.Join(avdHome, avdName+".ini")
		if iniBytes, err := os.ReadFile(iniPath); err == nil {
			if err := addZipBytes(zw, "avd.ini", iniBytes); err != nil {
				return err
			}
		}
		return filepath.WalkDir(avdDir, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.Type()&os.ModeSymlink != 0 {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(strings.ToLower(d.Name()), ".lock") {
				return nil
			}
			rel, err := filepath.Rel(avdDir, p)
			if err != nil {
				return err
			}
			zipRel := path.Join("avd", filepath.ToSlash(rel))
			return addZipFile(zw, zipRel, p)
		})
	}()
	closeZipErr := zw.Close()
	syncErr := tmpFile.Sync()
	closeFileErr := tmpFile.Close()
	if writeErr != nil {
		_ = os.Remove(tmpZip)
		return 0, writeErr
	}
	if closeZipErr != nil {
		_ = os.Remove(tmpZip)
		return 0, closeZipErr
	}
	if syncErr != nil {
		_ = os.Remove(tmpZip)
		return 0, syncErr
	}
	if closeFileErr != nil {
		_ = os.Remove(tmpZip)
		return 0, closeFileErr
	}
	if err := os.Rename(tmpZip, destZip); err != nil {
		_ = os.Remove(tmpZip)
		return 0, err
	}
	st, err := os.Stat(destZip)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func addZipBytes(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func addZipFile(zw *zip.Writer, name, diskPath string) error {
	info, err := os.Stat(diskPath)
	if err != nil {
		return err
	}
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = name
	hdr.Method = zip.Deflate
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	src, err := os.Open(diskPath)
	if err != nil {
		return err
	}
	defer src.Close()
	_, err = io.Copy(w, src)
	return err
}

const (
	maxBackupEntries            = 4096
	maxBackupExtractBytes int64 = 64 << 30 // 64 GiB
)

// RestoreAVD extracts a VirDeck backup zip into avdHome under newAVDName, rewriting ini paths cleanly.
func RestoreAVD(avdHome, newAVDName, srcZip string) (AVDBackupMeta, error) {
	if strings.TrimSpace(avdHome) == "" || !isSafePathSegment(newAVDName) {
		return AVDBackupMeta{}, fmt.Errorf("无效的目标 AVD 名称: %q", newAVDName)
	}
	zr, err := zip.OpenReader(srcZip)
	if err != nil {
		return AVDBackupMeta{}, fmt.Errorf("打开备份压缩包失败: %w", err)
	}
	defer zr.Close()

	if len(zr.File) > maxBackupEntries {
		return AVDBackupMeta{}, fmt.Errorf("备份压缩包包含过多条目（超过 %d 个）", maxBackupEntries)
	}

	var (
		meta     AVDBackupMeta
		hasMeta  bool
		iniBytes []byte
	)
	for _, f := range zr.File {
		if f.Mode()&os.ModeSymlink != 0 {
			return AVDBackupMeta{}, fmt.Errorf("备份包包含不受支持的符号链接条目: %s", f.Name)
		}
		if _, err := cleanBackupZipEntry(f.Name); err != nil {
			return AVDBackupMeta{}, err
		}
		if f.Name == "virdeck-backup.json" {
			data, err := readZipEntryBytes(f, 2*1024*1024)
			if err != nil {
				return AVDBackupMeta{}, fmt.Errorf("读取备份元数据失败: %w", err)
			}
			if err := json.Unmarshal(data, &meta); err != nil {
				return AVDBackupMeta{}, fmt.Errorf("备份元数据格式无效: %w", err)
			}
			hasMeta = true
		} else if f.Name == "avd.ini" {
			data, err := readZipEntryBytes(f, 256*1024)
			if err != nil {
				return AVDBackupMeta{}, err
			}
			iniBytes = data
		}
	}
	if !hasMeta {
		return AVDBackupMeta{}, errors.New("压缩包缺少 virdeck-backup.json，不是有效的 VirDeck 实例备份")
	}

	avdDir := filepath.Join(avdHome, newAVDName+".avd")
	iniPath := filepath.Join(avdHome, newAVDName+".ini")
	if _, err := os.Stat(avdDir); err == nil {
		return AVDBackupMeta{}, fmt.Errorf("目标 AVD 目录已存在: %s", newAVDName)
	}
	cleanup := func() {
		_ = os.RemoveAll(avdDir)
		_ = os.Remove(iniPath)
	}
	if err := os.MkdirAll(avdDir, 0o700); err != nil {
		return AVDBackupMeta{}, err
	}

	remaining := maxBackupExtractBytes
	for _, f := range zr.File {
		relClean, err := cleanBackupZipEntry(f.Name)
		if err != nil {
			cleanup()
			return AVDBackupMeta{}, err
		}
		if !strings.HasPrefix(relClean, "avd/") {
			continue
		}
		subRel := strings.TrimPrefix(relClean, "avd/")
		if subRel == "" || subRel == "." {
			continue
		}
		if strings.HasSuffix(strings.ToLower(subRel), ".lock") {
			continue
		}
		dst := filepath.Join(avdDir, filepath.FromSlash(subRel))
		if !isPathInsideBase(avdDir, dst) {
			cleanup()
			return AVDBackupMeta{}, fmt.Errorf("拒绝越界备份条目: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o700); err != nil {
				cleanup()
				return AVDBackupMeta{}, err
			}
			continue
		}
		if err := extractZipEntryFile(f, dst, &remaining); err != nil {
			cleanup()
			return AVDBackupMeta{}, err
		}
		if strings.HasSuffix(strings.ToLower(subRel), ".qcow2") {
			if backing, berr := readQcow2BackingFile(dst); berr != nil || (backing != "" && !isSafeBackingPath(backing, false)) {
				cleanup()
				if berr != nil {
					return AVDBackupMeta{}, fmt.Errorf("备份磁盘镜像校验失败 (%s): %w", subRel, berr)
				}
				return AVDBackupMeta{}, fmt.Errorf("备份磁盘镜像包含不安全的 backing-filename (%s): %q", subRel, backing)
			}
		}
	}

	if err := validateRestoredAVDConfigINI(filepath.Join(avdDir, "config.ini")); err != nil {
		cleanup()
		return AVDBackupMeta{}, err
	}

	target := extractTargetFromINI(iniBytes)
	if target == "" {
		target = deriveTargetFromSystemImage(meta.SystemImage)
	}
	if target != "" && (!isSafePathSegment(target) || !strings.HasPrefix(target, "android-")) {
		cleanup()
		return AVDBackupMeta{}, fmt.Errorf("备份包含非法的目标 API 标识: %q", target)
	}
	iniContent := fmt.Sprintf("avd.ini.encoding=UTF-8\npath=%s\npath.rel=%s\ntarget=%s\n",
		cleanIniLine(avdDir), cleanIniLine(filepath.Join("avd", newAVDName+".avd")), cleanIniLine(target))
	if err := os.WriteFile(iniPath, []byte(iniContent), 0o600); err != nil {
		cleanup()
		return AVDBackupMeta{}, err
	}
	return meta, nil
}

func cleanBackupZipEntry(name string) (string, error) {
	if strings.ContainsRune(name, '\x00') || strings.Contains(name, "\\") || strings.Contains(name, ":") {
		return "", fmt.Errorf("非法备份路径: %q", name)
	}
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("拒绝绝对路径备份条目: %q", name)
	}
	cleaned := path.Clean(name)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("拒绝路径穿越备份条目: %q", name)
	}
	for _, seg := range strings.Split(strings.TrimSuffix(cleaned, "/"), "/") {
		if seg == "" || seg == "." {
			continue
		}
		if strings.TrimRight(seg, ". ") != seg || isWindowsReservedSegment(seg) {
			return "", fmt.Errorf("非法备份路径段: %q", name)
		}
	}
	return cleaned, nil
}

func isWindowsReservedSegment(seg string) bool {
	stem, _, _ := strings.Cut(strings.ToUpper(seg), ".")
	stem = strings.TrimRight(stem, " ")
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) {
		return stem[3] >= '1' && stem[3] <= '9'
	}
	return false
}

func isSafeBackingPath(backing string, allowAbs bool) bool {
	if backing == "" || strings.HasPrefix(backing, "-") || strings.ContainsAny(backing, "\x00\r\n") {
		return false
	}
	lower := strings.ToLower(backing)
	if strings.Contains(backing, "://") || strings.HasPrefix(lower, "json:") || strings.HasPrefix(lower, "file:") {
		return false
	}
	norm := strings.ReplaceAll(backing, "\\", "/")
	for _, seg := range strings.Split(norm, "/") {
		if seg == ".." {
			return false
		}
	}
	if !allowAbs {
		if strings.HasPrefix(norm, "/") || (len(backing) >= 2 && backing[1] == ':') || filepath.IsAbs(backing) {
			return false
		}
		if !strings.HasSuffix(lower, ".img") && !strings.HasSuffix(lower, ".qcow2") {
			return false
		}
	}
	return true
}

func readQcow2BackingFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var hdr [24]byte
	n, err := io.ReadFull(f, hdr[:])
	if err != nil {
		// Small test fixtures or empty overlays without full header are ignored when magic isn't qcow2.
		return "", nil
	}
	if n < 24 || !bytes.Equal(hdr[0:4], []byte{'Q', 'F', 'I', 0xfb}) {
		return "", nil
	}
	off := binary.BigEndian.Uint64(hdr[8:16])
	sz := binary.BigEndian.Uint32(hdr[16:20])
	if off == 0 || sz == 0 {
		return "", nil
	}
	if sz > 4096 || off < 72 || off > 1<<20 {
		return "", errors.New("qcow2 backing-filename 偏移或长度异常")
	}
	buf := make([]byte, sz)
	if _, err := f.ReadAt(buf, int64(off)); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(buf)), nil
}

func validateRestoredAVDConfigINI(cfgPath string) error {
	data, err := os.ReadFile(cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(k))
		val := strings.TrimSpace(v)
		if val == "" {
			continue
		}
		if key == "image.sysdir.1" {
			norm := strings.Trim(strings.ReplaceAll(val, "\\", "/"), "/")
			parts := strings.Split(norm, "/")
			if len(parts) != 4 || parts[0] != "system-images" || !isSafePathSegment(parts[1]) || !isSafePathSegment(parts[2]) || !isSafePathSegment(parts[3]) {
				return fmt.Errorf("备份包 config.ini 包含非法的系统镜像路径: %q", val)
			}
			continue
		}
		if strings.HasSuffix(key, ".path") || strings.HasPrefix(key, "image.sysdir.") {
			return fmt.Errorf("备份包 config.ini 包含不允许的宿主路径键 %s", k)
		}
	}
	return nil
}

func isPathInsideBase(base, target string) bool {
	absBase, errB := filepath.Abs(base)
	absTarget, errT := filepath.Abs(target)
	if errB != nil || errT != nil {
		return false
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func readZipEntryBytes(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("备份条目 %s 超出大小限制", f.Name)
	}
	return data, nil
}

func extractZipEntryFile(f *zip.File, dst string, remaining *int64) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(out, io.LimitReader(rc, *remaining+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > *remaining {
		return fmt.Errorf("备份包解压总体积超过安全上限 (%d GiB)", maxBackupExtractBytes>>30)
	}
	*remaining -= n
	return nil
}

func extractTargetFromINI(data []byte) string {
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == "target" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func deriveTargetFromSystemImage(sysImg string) string {
	parts := strings.Split(strings.TrimSpace(sysImg), ";")
	if len(parts) >= 2 && strings.HasPrefix(parts[1], "android-") && utf8.ValidString(parts[1]) {
		return parts[1]
	}
	return ""
}

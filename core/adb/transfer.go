package adb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

type TransferEntry struct {
	Name  string
	IsDir bool
}

// SharedStoragePath rejects private guest paths, including normalized escapes.
func SharedStoragePath(p string) (string, error) {
	if strings.ContainsAny(p, "\x00\r\n") || !strings.HasPrefix(p, "/") {
		return "", errors.New("请选择 Android 共享存储中的路径")
	}
	p = path.Clean(p)
	if p == "/sdcard" || strings.HasPrefix(p, "/sdcard/") || p == "/storage/emulated/0" || strings.HasPrefix(p, "/storage/emulated/0/") {
		return p, nil
	}
	return "", errors.New("仅支持 /sdcard 或 /storage/emulated/0 共享存储")
}

func transferQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

type transferOutput struct {
	mu       sync.Mutex
	data     []byte
	overflow bool
}

func (w *transferOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	left := (64 << 10) - len(w.data)
	if n > left {
		w.overflow = true
		p = p[:left]
	}
	w.data = append(w.data, p...)
	return n, nil
}

// Transfer commands retain SDK/server isolation and cap both output and pipe waits.
func (c *Client) transferRun(ctx context.Context, args ...string) (string, error) {
	c.mu.RLock()
	bin := c.adbPath
	env := append([]string(nil), c.env...)
	c.mu.RUnlock()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = time.Second
	hideConsole(cmd)
	if len(env) > 0 {
		cmd.Env = env
	}
	out := &transferOutput{}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	text := string(out.data)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("ADB 文件传输: %w: %s", err, strings.TrimSpace(text))
	}
	if out.overflow {
		return "", errors.New("ADB 文件传输输出超过 64 KiB，请缩小目录范围")
	}
	return text, nil
}

const transferSharedGuard = `case "$r" in /storage/emulated/0|/storage/emulated/0/*) ;; *) echo '路径不在共享存储中' >&2; exit 1;; esac; `

func (c *Client) transferCanonical(ctx context.Context, port int, p string) (string, error) {
	p, err := SharedStoragePath(p)
	if err != nil {
		return "", err
	}
	out, err := c.transferRun(ctx, "-s", Serial(port), "shell", "readlink -f -- "+transferQuote(p))
	if err != nil {
		return "", err
	}
	out = strings.TrimSuffix(strings.TrimSuffix(out, "\n"), "\r")
	if out != "/storage/emulated/0" && !strings.HasPrefix(out, "/storage/emulated/0/") {
		return "", errors.New("远程路径实际指向共享存储之外")
	}
	return out, nil
}

// MediaScanWarning means all files arrived but Android's gallery refresh failed.
type MediaScanWarning struct{ Err error }

func (e *MediaScanWarning) Error() string {
	return "文件已发送，相册刷新失败: " + e.Err.Error()
}
func (e *MediaScanWarning) Unwrap() error { return e.Err }

// TransferSend reserves a fresh Android folder before pushing; neither existing
// guest files nor same-named Windows inputs can overwrite one another.
func (c *Client) TransferSend(ctx context.Context, port int, localPaths []string, remoteDir string) (string, error) {
	remoteDir, err := SharedStoragePath(remoteDir)
	if err != nil {
		return "", err
	}
	if len(localPaths) == 0 {
		return "", errors.New("请选择本地文件")
	}
	for _, p := range localPaths {
		info, e := os.Stat(p)
		if e != nil {
			return "", e
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("只支持普通文件: %s", p)
		}
	}
	script := "# virdeck:reserve\np=" + transferQuote(remoteDir) + `; q="$p"; while [ ! -e "$q" ]; do q=${q%/*}; [ -n "$q" ] || exit 1; done; r=$(readlink -f -- "$q") || exit; ` + transferSharedGuard + `mkdir -p -- "$p" || exit; r=$(readlink -f -- "$p") || exit; ` + transferSharedGuard + `mktemp -d "$r/` + time.Now().Format("20060102-150405") + `-XXXXXXXX"`
	out, err := c.transferRun(ctx, "-s", Serial(port), "shell", script)
	if err != nil {
		return "", err
	}
	dest := strings.TrimSuffix(strings.TrimSuffix(out, "\n"), "\r")
	if _, err = SharedStoragePath(dest); err != nil {
		return "", err
	}
	used := map[string]bool{}
	var scanErrors []error
	for _, p := range localPaths {
		abs, e := filepath.Abs(p)
		if e != nil {
			return dest, e
		}
		name := filepath.Base(p)
		base := strings.TrimSuffix(name, filepath.Ext(name))
		ext := filepath.Ext(name)
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s (%d)%s", base, n, ext)
		}
		used[name] = true
		if _, err = c.transferRun(ctx, "-s", Serial(port), "push", abs, path.Join(dest, name)); err != nil {
			return dest, fmt.Errorf("%s → %s: %w", p, dest, err)
		}
		uri := (&url.URL{Scheme: "file", Path: path.Join(dest, name)}).String()
		if _, e := c.transferRun(ctx, "-s", Serial(port), "shell", "am broadcast -a android.intent.action.MEDIA_SCANNER_SCAN_FILE -d "+transferQuote(uri)); e != nil {
			scanErrors = append(scanErrors, e)
		}
	}
	if len(scanErrors) > 0 {
		return dest, &MediaScanWarning{Err: errors.Join(scanErrors...)}
	}
	return dest, nil
}

func (c *Client) TransferReceive(ctx context.Context, port int, remotePath, localDir string) error {
	canonical, err := c.transferCanonical(ctx, port, remotePath)
	if err != nil {
		return err
	}
	// Enumerate without following symlinks. Validate the entire bounded manifest
	// before adb can map distinct Android names onto the same Windows file.
	classify := `for p do if [ -L "$p" ]; then t=l; elif [ -d "$p" ]; then t=d; elif [ -f "$p" ]; then t=f; else t=x; fi; printf '%s\000%s\000' "$t" "$p"; done`
	check := "# virdeck:receive-tree\nfind " + transferQuote(canonical) + " -exec sh -c " + transferQuote(classify) + " sh '{}' +"
	manifest, err := c.transferRun(ctx, "-s", Serial(port), "shell", check)
	if err != nil {
		return err
	}
	if err = validateTransferTree(canonical, manifest); err != nil {
		return err
	}
	_, err = c.transferRun(ctx, "-s", Serial(port), "pull", canonical, localDir)
	return err
}

var transferShortName = regexp.MustCompile(`(?i)^[^. ]{1,6}~[0-9]+(?:\.[^. ]{0,3})?$`)

func validateTransferName(name string) error {
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) || strings.ContainsAny(name, `<>:"/\|?*`) || strings.TrimRight(name, ". ") != name || len(utf16.Encode([]rune(name))) > 255 {
		return fmt.Errorf("Windows 不支持文件名 %q", name)
	}
	for _, r := range name {
		if r < 32 {
			return fmt.Errorf("Windows 不支持文件名 %q", name)
		}
	}
	stem, _, _ := strings.Cut(strings.ToUpper(name), ".")
	stem = strings.TrimRight(stem, " ")
	reserved := stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$"
	if strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT") {
		suffix := stem[3:]
		reserved = reserved || strings.Contains("123456789¹²³", suffix) && utf8.RuneCountInString(suffix) == 1
	}
	if reserved || transferShortName.MatchString(name) {
		return fmt.Errorf("文件名可能映射到 Windows 保留名称或短名称: %q", name)
	}
	return nil
}

func transferWindowsKey(name string) string {
	return strings.Map(func(r rune) rune {
		r = unicode.ToUpper(r)
		smallest := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < smallest {
				smallest = next
			}
		}
		return smallest
	}, name)
}

func validateTransferTree(root, manifest string) error {
	fields := strings.Split(manifest, "\x00")
	if len(fields) < 3 || len(fields)%2 != 1 || fields[len(fields)-1] != "" {
		return errors.New("ADB 导出目录清单不完整")
	}
	seen := map[string]string{}
	foundRoot := false
	for i := 0; i+1 < len(fields); i += 2 {
		kind, p := fields[i], fields[i+1]
		if kind != "d" && kind != "f" {
			return fmt.Errorf("不能安全导出符号链接或特殊文件: %q", p)
		}
		if p != root && !strings.HasPrefix(p, root+"/") || path.Clean(p) != p {
			return fmt.Errorf("导出目录清单包含越界路径: %q", p)
		}
		rel := path.Base(root)
		if p != root {
			rel += "/" + strings.TrimPrefix(p, root+"/")
		} else {
			foundRoot = true
		}
		for _, part := range strings.Split(rel, "/") {
			if err := validateTransferName(part); err != nil {
				return fmt.Errorf("无法保留 Android 路径 %q: %w", p, err)
			}
		}
		key := transferWindowsKey(rel)
		if previous, ok := seen[key]; ok {
			return fmt.Errorf("Android 路径 %q 与 %q 在 Windows 中重名，已取消导出", previous, p)
		}
		seen[key] = p
	}
	if !foundRoot {
		return errors.New("ADB 导出目录清单缺少源路径")
	}
	return nil
}

func (c *Client) TransferList(ctx context.Context, port int, remoteDir string) ([]TransferEntry, error) {
	canonical, err := c.transferCanonical(ctx, port, remoteDir)
	if err != nil {
		return nil, err
	}
	script := "# virdeck:list\ncd " + transferQuote(canonical) + ` || exit; for p in ./* ./.[!.]* ./..?*; do [ -e "$p" ] || continue; [ ! -L "$p" ] || continue; if [ -d "$p" ]; then printf 'd\000%s\000' "${p#./}"; elif [ -f "$p" ]; then printf 'f\000%s\000' "${p#./}"; fi; done`
	out, err := c.transferRun(ctx, "-s", Serial(port), "shell", script)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(out, "\x00")
	entries := []TransferEntry{}
	for i := 0; i+1 < len(fields); i += 2 {
		if (fields[i] != "d" && fields[i] != "f") || strings.Contains(fields[i+1], "/") {
			return nil, errors.New("ADB 目录列表格式异常")
		}
		entries = append(entries, TransferEntry{Name: fields[i+1], IsDir: fields[i] == "d"})
	}
	return entries, nil
}

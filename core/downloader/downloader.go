package downloader

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"virdeck/config"
)

var (
	// GoogleRepositoryXMLURL is the official Android SDK main repository manifest.
	GoogleRepositoryXMLURL = config.Values.Sources.RepositoryXML
	// GoogleSysImgPlayStoreXMLURL is the official Google Play Store system image manifest.
	GoogleSysImgPlayStoreXMLURL = config.Values.Sources.PlayStoreXML
	// GoogleSysImgGoogleAPIsXMLURL is the official Google APIs system image manifest.
	GoogleSysImgGoogleAPIsXMLURL = config.Values.Sources.GoogleAPIsXML
	// GoogleSysImgDefaultXMLURL is the official AOSP default system image manifest.
	GoogleSysImgDefaultXMLURL = config.Values.Sources.DefaultXML

	// FallbackPlatformToolsURL is the stable official fallback for Windows platform-tools.
	FallbackPlatformToolsURL = config.Values.Sources.PlatformTools
	// FallbackScrcpyURL is the stable official fallback for scrcpy win64 release.
	FallbackScrcpyURL = config.Values.Sources.Scrcpy
)

// Progress describes the current status of a download/extract job.
type Progress struct {
	TargetID        string  `json:"targetId"`
	Name            string  `json:"name"`
	Stage           string  `json:"stage"` // "downloading" | "verifying" | "extracting" | "completed" | "failed"
	DownloadedBytes int64   `json:"downloadedBytes"`
	TotalBytes      int64   `json:"totalBytes"`
	Percent         float64 `json:"percent"`
	SpeedBytesPerS  int64   `json:"speedBytesPerSec"`
	Error           string  `json:"error,omitempty"`
}

// ProgressFunc receives progress callbacks during download, checksum verification, and extraction.
type ProgressFunc func(Progress)

// RemoteArchive describes a downloadable archive resolved from an official XML manifest or fallback table.
type RemoteArchive struct {
	PackagePath string `json:"packagePath"`
	DisplayName string `json:"displayName"`
	URL         string `json:"url"`
	Size        int64  `json:"size"`
	SHA1        string `json:"sha1"`
	Revision    string `json:"revision"`
	APILevel    string `json:"apiLevel,omitempty"`
	TagID       string `json:"tagId,omitempty"`
	TagDisplay  string `json:"tagDisplay,omitempty"`
	ABI         string `json:"abi,omitempty"`
}

type partMeta struct {
	URL          string `json:"url"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	TotalBytes   int64  `json:"totalBytes"`
	SHA1         string `json:"sha1,omitempty"`
}

// Client handles proxy-aware HTTP downloads with Range/If-Range resume, SHA-1 verification, and atomic zip extraction.
type Client struct {
	proxyMu      sync.RWMutex
	idleTimeout  time.Duration
	downloadsDir string
	proxyURL     string
}

// New creates a new Downloader client storing temporary .part/.meta files in downloadsDir.
func New(downloadsDir, proxyURL string) *Client {
	return &Client{
		downloadsDir: downloadsDir,
		idleTimeout:  time.Duration(config.Values.Runtime.DownloadIdleSeconds) * time.Second,
		proxyURL:     strings.TrimSpace(proxyURL),
	}
}

// SetProxy updates the HTTP/SOCKS5 proxy URL used for downloads and XML manifest queries.
func (c *Client) SetProxy(proxyURL string) {
	c.proxyMu.Lock()
	defer c.proxyMu.Unlock()
	c.proxyURL = strings.TrimSpace(proxyURL)
}

// Proxy returns the currently configured download proxy URL.
func (c *Client) Proxy() string {
	c.proxyMu.RLock()
	defer c.proxyMu.RUnlock()
	return c.proxyURL
}

func (c *Client) httpClient(timeout time.Duration) (*http.Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = c.idleTimeout
	proxy := c.Proxy()
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("解析下载代理地址 %q 失败: %w", proxy, err)
		}
		tr.Proxy = http.ProxyURL(u)
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}, nil
}

// Download resumes or fetches urlStr into a verified local file inside downloadsDir.
// If expectedSHA1 is non-empty, the completed file is strictly verified against it.
func (c *Client) Download(ctx context.Context, targetID, name, urlStr, expectedSHA1 string, onProgress ProgressFunc) (string, error) {
	if strings.TrimSpace(urlStr) == "" {
		return "", errors.New("下载 URL 不能为空")
	}
	if err := os.MkdirAll(c.downloadsDir, 0o755); err != nil {
		return "", fmt.Errorf("创建下载缓存目录失败: %w", err)
	}

	parsed, err := url.Parse(urlStr)
	if err != nil {
		return "", fmt.Errorf("无效的下载 URL: %w", err)
	}
	baseName := sanitizeFileName(filepath.Base(parsed.Path))
	if baseName == "" || baseName == "." || baseName == ".." || baseName == "archive" {
		baseName = sanitizeFileName(targetID) + ".zip"
	}
	finalPath := filepath.Join(c.downloadsDir, baseName)
	partPath := finalPath + ".part"
	metaPath := finalPath + ".meta"

	// If the completed archive already exists and passes SHA-1, reuse it directly.
	if info, err := os.Stat(finalPath); err == nil && !info.IsDir() && info.Size() > 0 {
		if expectedSHA1 == "" {
			emit(onProgress, Progress{
				TargetID:        targetID,
				Name:            name,
				Stage:           "completed",
				DownloadedBytes: info.Size(),
				TotalBytes:      info.Size(),
				Percent:         100,
			})
			return finalPath, nil
		}
		if ok, _ := VerifyFileSHA1(finalPath, expectedSHA1); ok {
			emit(onProgress, Progress{
				TargetID:        targetID,
				Name:            name,
				Stage:           "completed",
				DownloadedBytes: info.Size(),
				TotalBytes:      info.Size(),
				Percent:         100,
			})
			return finalPath, nil
		}
		_ = os.Remove(finalPath)
	}

	meta := loadPartMeta(metaPath)
	var offset int64
	if st, err := os.Stat(partPath); err == nil && !st.IsDir() {
		offset = st.Size()
	}
	if meta.URL != "" && meta.URL != urlStr {
		offset = 0
		_ = os.Remove(partPath)
		_ = os.Remove(metaPath)
		meta = partMeta{}
	}

	hc, err := c.httpClient(0)
	if err != nil {
		return "", err
	}

	watch := newDownloadWatch(ctx, c.idleTimeout)
	defer watch.close()
	req, err := http.NewRequestWithContext(watch.ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		if meta.ETag != "" {
			req.Header.Set("If-Range", meta.ETag)
		} else if meta.LastModified != "" {
			req.Header.Set("If-Range", meta.LastModified)
		}
	}

	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求下载失败: %w", watch.reason(err))
	}
	resp.Body = &downloadIdleBody{ReadCloser: resp.Body, watch: watch}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Server ignored Range or started fresh; truncate .part from 0.
		offset = 0
	case http.StatusPartialContent:
		// Resuming from offset.
	case http.StatusRequestedRangeNotSatisfiable:
		// .part may already be complete or out of range; restart cleanly.
		_ = os.Remove(partPath)
		_ = os.Remove(metaPath)
		return c.Download(ctx, targetID, name, urlStr, expectedSHA1, onProgress)
	default:
		return "", fmt.Errorf("下载服务器返回异常状态码: %d %s", resp.StatusCode, resp.Status)
	}

	total := resp.ContentLength
	if total > 0 {
		total += offset
	} else if meta.TotalBytes > 0 {
		total = meta.TotalBytes
	}

	newMeta := partMeta{
		URL:          urlStr,
		ETag:         strings.TrimSpace(resp.Header.Get("ETag")),
		LastModified: strings.TrimSpace(resp.Header.Get("Last-Modified")),
		TotalBytes:   total,
		SHA1:         expectedSHA1,
	}
	_ = savePartMeta(metaPath, newMeta)

	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	outFile, err := os.OpenFile(partPath, flags, 0o644)
	if err != nil {
		return "", fmt.Errorf("打开临时文件失败: %w", err)
	}

	buf := make([]byte, 64*1024)
	written := offset
	startTime := time.Now()
	lastReport := time.Time{}

	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := outFile.Write(buf[:n]); werr != nil {
				_ = outFile.Close()
				return "", fmt.Errorf("写入下载文件失败: %w", werr)
			}
			written += int64(n)
			now := time.Now()
			if now.Sub(lastReport) >= 120*time.Millisecond || rerr == io.EOF {
				lastReport = now
				elapsed := now.Sub(startTime).Seconds()
				var speed int64
				if elapsed > 0 {
					speed = int64(float64(written-offset) / elapsed)
				}
				var pct float64
				if total > 0 {
					pct = float64(written) * 100 / float64(total)
				}
				emit(onProgress, Progress{
					TargetID:        targetID,
					Name:            name,
					Stage:           "downloading",
					DownloadedBytes: written,
					TotalBytes:      total,
					Percent:         pct,
					SpeedBytesPerS:  speed,
				})
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			_ = outFile.Close()
			return "", fmt.Errorf("下载中断: %w", rerr)
		}
	}
	if err := outFile.Close(); err != nil {
		return "", err
	}

	if expectedSHA1 != "" {
		emit(onProgress, Progress{
			TargetID:        targetID,
			Name:            name,
			Stage:           "verifying",
			DownloadedBytes: written,
			TotalBytes:      total,
			Percent:         100,
		})
		ok, gotSum := VerifyFileSHA1(partPath, expectedSHA1)
		if !ok {
			_ = os.Remove(partPath)
			_ = os.Remove(metaPath)
			return "", fmt.Errorf("SHA-1 校验失败: 期望 %s，实际 %s", expectedSHA1, gotSum)
		}
	}

	_ = os.Remove(finalPath)
	if err := os.Rename(partPath, finalPath); err != nil {
		return "", fmt.Errorf("重命名下载完成文件失败: %w", err)
	}
	_ = os.Remove(metaPath)
	return finalPath, nil
}

// VerifyFileSHA1 computes the SHA-1 hex digest of filePath and compares it against expectedHex (case-insensitive).
// If expectedHex is empty, it returns true along with the computed digest.
func VerifyFileSHA1(filePath, expectedHex string) (bool, string) {
	f, err := os.Open(filePath)
	if err != nil {
		return false, ""
	}
	defer f.Close()

	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, ""
	}
	got := hex.EncodeToString(h.Sum(nil))
	expectedHex = strings.TrimSpace(expectedHex)
	if expectedHex == "" {
		return true, got
	}
	return strings.EqualFold(got, expectedHex), got
}

// ExtractZipAtomically extracts zipPath into destDir via a sibling temporary folder,
// stripping a single common top-level directory if present and guarding against Zip Slip path traversal.
func ExtractZipAtomically(ctx context.Context, zipPath, destDir string, onProgress ProgressFunc, targetID, name string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("打开压缩包 %s 失败: %w", filepath.Base(zipPath), err)
	}
	defer r.Close()

	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return err
	}
	tmpDir := destDir + ".extract_tmp"
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("创建临时解压目录失败: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	stripPrefix := detectSingleRootPrefix(r.File)
	entries, err := validateZipEntries(r.File, stripPrefix)
	if err != nil {
		return err
	}
	cleanTmpRoot := filepath.Clean(tmpDir) + string(os.PathSeparator)

	totalFiles := len(r.File)
	for i, f := range r.File {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		relName := entries[f]
		if relName == "" {
			continue
		}

		targetPath := filepath.Join(tmpDir, filepath.FromSlash(relName))
		cleanTarget := filepath.Clean(targetPath)
		if !strings.HasPrefix(cleanTarget, cleanTmpRoot) && cleanTarget != filepath.Clean(tmpDir) {
			return fmt.Errorf("压缩包包含非法越界路径 (Zip Slip): %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(cleanTarget, 0o755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o755); err != nil {
			return err
		}
		if err := extractSingleZipEntry(f, cleanTarget); err != nil {
			return err
		}

		if totalFiles > 0 && (i%10 == 0 || i == totalFiles-1) {
			emit(onProgress, Progress{
				TargetID: targetID,
				Name:     name,
				Stage:    "extracting",
				Percent:  float64(i+1) * 100 / float64(totalFiles),
			})
		}
	}

	backupDir := destDir + ".old_tmp"
	_ = os.RemoveAll(backupDir)
	if _, err := os.Stat(destDir); err == nil {
		if err := os.Rename(destDir, backupDir); err != nil {
			_ = os.RemoveAll(destDir)
		}
	}
	if err := os.Rename(tmpDir, destDir); err != nil {
		if _, berr := os.Stat(backupDir); berr == nil {
			_ = os.Rename(backupDir, destDir)
		}
		return fmt.Errorf("原子替换解压目录失败: %w", err)
	}
	_ = os.RemoveAll(backupDir)

	emit(onProgress, Progress{
		TargetID: targetID,
		Name:     name,
		Stage:    "completed",
		Percent:  100,
	})
	return nil
}

func ExtractArchiveAtomically(ctx context.Context, archivePath, destDir string, onProgress ProgressFunc, targetID, name string) error {
	if !strings.HasSuffix(strings.ToLower(archivePath), ".tar.gz") && !strings.HasSuffix(strings.ToLower(archivePath), ".tgz") {
		return ExtractZipAtomically(ctx, archivePath, destDir, onProgress, targetID, name)
	}
	// Normalize tar entries into ZIP metadata, sharing one security preflight
	// and atomic installation path for both official distribution formats.
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tmp, err := os.CreateTemp("", "virdeck-archive-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	zw := zip.NewWriter(tmp)
	tr := tar.NewReader(gz)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		zh := &zip.FileHeader{Name: h.Name, Method: zip.Deflate}
		zh.SetMode(h.FileInfo().Mode())
		switch h.Typeflag {
		case tar.TypeReg, tar.TypeRegA, tar.TypeDir, tar.TypeSymlink:
		default:
			return fmt.Errorf("不支持的 tar 条目: %s", h.Name)
		}
		out, err := zw.CreateHeader(zh)
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeSymlink {
			_, err = io.WriteString(out, h.Linkname)
		} else {
			_, err = io.Copy(out, tr)
		}
		if err != nil {
			return err
		}
	}
	if err = zw.Close(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return ExtractZipAtomically(ctx, tmp.Name(), destDir, onProgress, targetID, name)
}

func extractSingleZipEntry(f *zip.File, dstPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	mode := f.Mode()
	if mode&os.ModeSymlink != 0 {
		target, err := io.ReadAll(io.LimitReader(rc, 64*1024+1))
		if err != nil {
			return err
		}
		return os.Symlink(string(target), dstPath)
	}
	mode = mode.Perm()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, rc); err != nil {
		return err
	}
	// Explicit chmod preserves executable bits even with a restrictive umask.
	return out.Chmod(mode)
}

// Validate the complete entry graph before writing anything: archive order must
// never allow a file to be written through a link, or a later link to replace a
// previously populated directory. Framework links may point through other links.
func validateZipEntries(files []*zip.File, prefix string) (map[*zip.File]string, error) {
	entries := make(map[*zip.File]string, len(files))
	links := make(map[string]string)
	seen := make(map[string]bool)
	for _, f := range files {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if !safeArchivePath(name) {
			return nil, fmt.Errorf("压缩包包含非法路径: %s", f.Name)
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(name, prefix), "/")
		if rel == "" {
			continue
		}
		key := archivePathKey(rel)
		if seen[key] {
			return nil, fmt.Errorf("压缩包包含重复路径: %s", rel)
		}
		seen[key] = true
		entries[f] = rel
		if f.Mode()&os.ModeSymlink != 0 {
			r, err := f.Open()
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(io.LimitReader(r, 64*1024+1))
			r.Close()
			if err != nil {
				return nil, err
			}
			target := string(data)
			if len(data) > 64*1024 || target == "" || strings.ContainsAny(target, "\\\x00:") || path.IsAbs(target) {
				return nil, fmt.Errorf("非法符号链接: %s", rel)
			}
			links[key] = target
		} else if !f.Mode().IsRegular() && !f.FileInfo().IsDir() {
			return nil, fmt.Errorf("不支持的压缩包条目: %s", rel)
		}
	}
	for _, rel := range entries {
		for parent := path.Dir(rel); parent != "."; parent = path.Dir(parent) {
			if _, ok := links[archivePathKey(parent)]; ok {
				return nil, fmt.Errorf("压缩包路径穿过符号链接: %s", rel)
			}
		}
	}
	for rel, target := range links {
		if err := validateLinkTarget(path.Dir(rel)+"/"+target, links, map[string]bool{rel: true}); err != nil {
			return nil, fmt.Errorf("非法符号链接 %s: %w", rel, err)
		}
	}
	return entries, nil
}

func safeArchivePath(name string) bool {
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\x00:") {
		return false
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == "" || segment == ".." || segment == "." || strings.TrimRight(segment, ". ") != segment {
			return false
		}
	}
	return true
}

func validateLinkTarget(target string, links map[string]string, visiting map[string]bool) error {
	if path.IsAbs(target) {
		return errors.New("链接目标越界")
	}
	parts := strings.Split(target, "/")
	var resolvedParts []string
	for i := range parts {
		if parts[i] == "." || parts[i] == "" {
			continue
		}
		if parts[i] == ".." {
			if len(resolvedParts) == 0 {
				return errors.New("链接目标越界")
			}
			resolvedParts = resolvedParts[:len(resolvedParts)-1]
			continue
		}
		if strings.TrimRight(parts[i], ". ") != parts[i] {
			return errors.New("链接目标越界")
		}
		resolvedParts = append(resolvedParts, parts[i])
		candidate := archivePathKey(strings.Join(resolvedParts, "/"))
		if next, ok := links[candidate]; ok {
			if visiting[candidate] {
				return errors.New("循环符号链接")
			}
			visiting[candidate] = true
			resolved := path.Dir(candidate) + "/" + next + "/" + strings.Join(parts[i+1:], "/")
			return validateLinkTarget(resolved, links, visiting)
		}
	}
	return nil
}

// APFS/HFS+ commonly compare names without case or Unicode normalization.
// Apply the conservative identity on every host, so validation is portable.
func archivePathKey(name string) string { return norm.NFD.String(cases.Fold().String(name)) }

func detectSingleRootPrefix(files []*zip.File) string {
	var root string
	for _, f := range files {
		rel := strings.TrimPrefix(filepath.ToSlash(f.Name), "/")
		if rel == "" {
			continue
		}
		parts := strings.SplitN(rel, "/", 2)
		if len(parts) < 2 {
			// There is a file right at the root of the archive; do not strip.
			if !f.FileInfo().IsDir() {
				return ""
			}
			continue
		}
		if root == "" {
			root = parts[0] + "/"
		} else if root != parts[0]+"/" {
			return ""
		}
	}
	return root
}

// FetchRepositoryPackages resolves official repository packages for the current host.
func (c *Client) FetchRepositoryPackages(ctx context.Context, xmlURL string) ([]RemoteArchive, error) {
	return c.FetchRepositoryPackagesForHost(ctx, xmlURL, runtime.GOOS, runtime.GOARCH)
}

// FetchRepositoryPackagesForHost resolves packages for an explicitly selected host.
func (c *Client) FetchRepositoryPackagesForHost(ctx context.Context, xmlURL, goos, goarch string) ([]RemoteArchive, error) {
	hc, err := c.httpClient(25 * time.Second)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, xmlURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取仓库索引 %s 失败: HTTP %d", xmlURL, resp.StatusCode)
	}
	const maxManifestBytes = 64 << 20 // 64 MiB
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("仓库索引 %s 超过大小上限 (%d MiB)", xmlURL, maxManifestBytes>>20)
	}
	baseURL := xmlURL[:strings.LastIndex(xmlURL, "/")+1]
	return ParseRepositoryXMLForHost(data, baseURL, goos, goarch)
}

type xmlRepository struct {
	Packages []xmlRemotePackage `xml:"remotePackage"`
}

type xmlChannelRef struct {
	Ref string `xml:"ref,attr"`
}

type xmlRemotePackage struct {
	Channel     xmlChannelRef  `xml:"channelRef"`
	Path        string         `xml:"path,attr"`
	DisplayName string         `xml:"display-name"`
	Revision    xmlRevision    `xml:"revision"`
	TypeDetails xmlTypeDetails `xml:"type-details"`
	Archives    []xmlArchive   `xml:"archives>archive"`
}

type xmlRevision struct {
	Preview int `xml:"preview"`
	Major   int `xml:"major"`
	Minor   int `xml:"minor"`
	Micro   int `xml:"micro"`
}

type xmlTypeDetails struct {
	APILevel string `xml:"api-level"`
	Tag      xmlTag `xml:"tag"`
	ABI      string `xml:"abi"`
	ABIs     string `xml:"abis"`
}

type xmlTag struct {
	ID      string `xml:"id"`
	Display string `xml:"display"`
}

type xmlArchive struct {
	HostArch string      `xml:"host-arch"`
	HostOS   string      `xml:"host-os"`
	HostBits string      `xml:"host-bits"`
	Complete xmlComplete `xml:"complete"`
}

type xmlComplete struct {
	Size     int64  `xml:"size"`
	Checksum string `xml:"checksum"`
	URL      string `xml:"url"`
}

// ParseRepositoryXML parses Android SDK repository2-3.xml / sys-img2-3.xml bytes and resolves archive URLs against baseURL.
func ParseRepositoryXML(data []byte, baseURL string) ([]RemoteArchive, error) {
	return ParseRepositoryXMLForHost(data, baseURL, runtime.GOOS, runtime.GOARCH)
}

// ParseRepositoryXMLForHost matches host OS, bitness and architecture, preferring
// exact host archives over OS-independent archives.
func ParseRepositoryXMLForHost(data []byte, baseURL, goos, goarch string) ([]RemoteArchive, error) {
	var repo xmlRepository
	if err := xml.Unmarshal(data, &repo); err != nil {
		return nil, fmt.Errorf("解析 Android SDK XML 索引失败: %w", err)
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}

	var results []RemoteArchive
	for _, pkg := range repo.Packages {
		if (pkg.Channel.Ref != "" && pkg.Channel.Ref != "channel-0") || pkg.Revision.Preview > 0 || IsPrerelease(pkg.Path+" "+pkg.DisplayName) {
			continue
		}
		var chosen *xmlArchive
		bestScore := -1
		for i := range pkg.Archives {
			a := &pkg.Archives[i]
			if score := archiveHostScore(*a, pkg.Path, goos, goarch); score > bestScore {
				chosen, bestScore = a, score
			}
		}
		if chosen == nil || strings.TrimSpace(chosen.Complete.URL) == "" {
			continue
		}
		u := strings.TrimSpace(chosen.Complete.URL)
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			u = baseURL + strings.TrimPrefix(u, "/")
		}
		rev := strconv.Itoa(pkg.Revision.Major)
		if pkg.Revision.Minor > 0 || pkg.Revision.Micro > 0 {
			rev = fmt.Sprintf("%d.%d.%d", pkg.Revision.Major, pkg.Revision.Minor, pkg.Revision.Micro)
		}
		abi := strings.TrimSpace(pkg.TypeDetails.ABI)
		if abi == "" {
			abi = strings.TrimSpace(pkg.TypeDetails.ABIs)
		}
		results = append(results, RemoteArchive{
			PackagePath: strings.TrimSpace(pkg.Path),
			DisplayName: strings.TrimSpace(pkg.DisplayName),
			URL:         u,
			Size:        chosen.Complete.Size,
			SHA1:        strings.TrimSpace(chosen.Complete.Checksum),
			Revision:    rev,
			APILevel:    strings.TrimSpace(pkg.TypeDetails.APILevel),
			TagID:       strings.TrimSpace(pkg.TypeDetails.Tag.ID),
			TagDisplay:  strings.TrimSpace(pkg.TypeDetails.Tag.Display),
			ABI:         abi,
		})
	}
	return results, nil
}

func archiveHostScore(a xmlArchive, pkg, goos, goarch string) int {
	host := strings.ToLower(strings.TrimSpace(a.HostOS))
	if goos == "darwin" {
		goos = "macosx"
	}
	if host != "" && host != goos {
		return -1
	}
	bits := strings.TrimSpace(a.HostBits)
	if bits != "" && bits != "64" {
		return -1
	}
	arch := strings.ToLower(strings.TrimSpace(a.HostArch))
	switch arch {
	case "x64", "x86_64":
		arch = "amd64"
	case "aarch64":
		arch = "arm64"
	}
	if arch != "" && arch != goarch {
		return -1
	}
	if arch == "" && host != "" && !strings.HasPrefix(pkg, "system-images;") {
		// SDK archives without host-arch are historically x64, except the
		// universal macOS platform-tools distribution. Never assume Linux ARM.
		if goarch != "amd64" && !(goarch == "arm64" && host == "macosx" && pkg == "platform-tools") {
			return -1
		}
	}
	score := 0
	if host != "" {
		score += 2
	}
	if arch != "" {
		score += 1
	}
	return score
}

func loadPartMeta(path string) partMeta {
	data, err := os.ReadFile(path)
	if err != nil {
		return partMeta{}
	}
	var m partMeta
	_ = json.Unmarshal(data, &m)
	return m
}

func savePartMeta(path string, m partMeta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func sanitizeFileName(s string) string {
	r := strings.NewReplacer(";", "_", "/", "_", "\\", "_", ":", "_", " ", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	out := r.Replace(strings.TrimSpace(s))
	if out == "" || out == "." || out == ".." {
		return "archive"
	}
	return out
}

func emit(fn ProgressFunc, p Progress) {
	if fn != nil {
		fn(p)
	}
}

// IsPrerelease recognizes explicit prerelease labels, including old cached names.
func IsPrerelease(value string) bool {
	for _, word := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return r < 'a' || r > 'z' }) {
		switch word {
		case "beta", "alpha", "preview", "canary", "rc":
			return true
		}
	}
	return false
}

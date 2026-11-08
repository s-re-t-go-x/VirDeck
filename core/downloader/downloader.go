package downloader

import (
	"archive/zip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	baseName := filepath.Base(parsed.Path)
	if baseName == "" || baseName == "." || baseName == "/" {
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
	cleanTmpRoot := filepath.Clean(tmpDir) + string(os.PathSeparator)

	totalFiles := len(r.File)
	for i, f := range r.File {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		relName := strings.TrimPrefix(filepath.ToSlash(f.Name), "/")
		if stripPrefix != "" {
			relName = strings.TrimPrefix(relName, stripPrefix)
		}
		relName = strings.TrimPrefix(relName, "/")
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

func extractSingleZipEntry(f *zip.File, dstPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	mode := f.Mode()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}

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

// FetchRepositoryPackages parses an official Android SDK repository2 / sys-img2 XML URL and returns Windows (or OS-agnostic) packages.
func (c *Client) FetchRepositoryPackages(ctx context.Context, xmlURL string) ([]RemoteArchive, error) {
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
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	baseURL := xmlURL[:strings.LastIndex(xmlURL, "/")+1]
	return ParseRepositoryXML(data, baseURL)
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
		for i := range pkg.Archives {
			a := &pkg.Archives[i]
			host := strings.ToLower(strings.TrimSpace(a.HostOS))
			bits := strings.TrimSpace(a.HostBits)
			if host == "windows" && (bits == "" || bits == "64") {
				chosen = a
				break
			}
			if host == "" && chosen == nil {
				chosen = a
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
	r := strings.NewReplacer(";", "_", "/", "_", "\\", "_", ":", "_", " ", "_")
	out := r.Replace(strings.TrimSpace(s))
	if out == "" {
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

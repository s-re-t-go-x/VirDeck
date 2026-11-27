package apk

import (
	"archive/zip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf16"

	"virdeck/core/catalog"
)

const (
	resStringPoolType       = 0x0001
	resXMLType              = 0x0003
	resXMLStartElementType  = 0x0102
	stringPoolUTF8Flag      = 0x0100
	typedValueStringType    = 0x03
	maxManifestReadBytes    = 4 << 20
	maxStringPoolEntryCount = 1 << 20
	maxDecodedPoolBytes     = 16 << 20
	maxTotalZipEntries      = 4096
	maxSplitBundleEntries   = 128
	maxSplitExtractBytes    = int64(2) << 30
)

var (
	validAndroidPkgRe  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)
	errSplitSizeLimit  = errors.New("分包解压总体积超出 2 GiB 安全限制")
)

func isValidAndroidPkg(pkg string) bool {
	return validAndroidPkgRe.MatchString(pkg)
}

// Metadata contains lightweight package and native ABI metadata extracted from an APK or split bundle.
type Metadata struct {
	Path       string   `json:"path"`
	Package    string   `json:"package"`
	ABIs       []string `json:"abis,omitempty"`
	IsSplit    bool     `json:"isSplit,omitempty"`
	SplitCount int      `json:"splitCount,omitempty"`
	HasOBB     bool     `json:"hasOBB,omitempty"`
}

// IsSplitBundlePath reports whether path points to a supported split-APK bundle (.apks or .xapk).
func IsSplitBundlePath(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".apks", ".xapk":
		return true
	default:
		return false
	}
}

// IsSupportedPackagePath reports whether path has a supported Android package extension (.apk, .apks, .xapk).
func IsSupportedPackagePath(path string) bool {
	trimmed := strings.TrimSpace(path)
	if strings.EqualFold(filepath.Ext(trimmed), ".apk") {
		return true
	}
	return IsSplitBundlePath(trimmed)
}

// Inspect reads an APK or split-APK bundle (.apks / .xapk) offline in pure Go and extracts
// lib/<abi>/ directories along with the package name from binary AndroidManifest.xml.
func Inspect(apkPath string) (Metadata, error) {
	apkPath = strings.TrimSpace(apkPath)
	if apkPath == "" || strings.HasPrefix(apkPath, "-") {
		return Metadata{}, fmt.Errorf("无效的 APK 路径: %q", apkPath)
	}
	if IsSplitBundlePath(apkPath) {
		return inspectSplitBundle(apkPath)
	}
	return inspectSingleAPK(apkPath)
}

func inspectSingleAPK(apkPath string) (Metadata, error) {
	pkg, _, abis, err := inspectSingleAPKFull(apkPath)
	if err != nil {
		return Metadata{}, err
	}
	return Metadata{
		Path:    apkPath,
		Package: pkg,
		ABIs:    abis,
	}, nil
}

func inspectSingleAPKFull(apkPath string) (pkg, splitName string, abis []string, err error) {
	r, err := zip.OpenReader(apkPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("无法打开 APK 压缩包: %w", err)
	}
	defer r.Close()
	return inspectZipReaderAPK(&r.Reader)
}

func inspectZipReaderAPK(r *zip.Reader) (pkg, splitName string, abis []string, err error) {
	abiSet := make(map[string]struct{})
	var manifestFile *zip.File

	for _, f := range r.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if name == "AndroidManifest.xml" && manifestFile == nil {
			manifestFile = f
		}
		if strings.HasPrefix(name, "lib/") {
			parts := strings.Split(name, "/")
			if len(parts) >= 3 && parts[1] != "" && !f.FileInfo().IsDir() {
				abiSet[parts[1]] = struct{}{}
			}
		}
	}

	if manifestFile == nil {
		return "", "", nil, errors.New("APK 缺少 AndroidManifest.xml，可能不是有效的 Android 安装包")
	}

	rc, err := manifestFile.Open()
	if err != nil {
		return "", "", nil, fmt.Errorf("读取 AndroidManifest.xml 失败: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(rc, maxManifestReadBytes+1))
	_ = rc.Close()
	if readErr != nil {
		return "", "", nil, fmt.Errorf("读取 AndroidManifest.xml 内容失败: %w", readErr)
	}
	if len(data) > maxManifestReadBytes {
		return "", "", nil, errors.New("AndroidManifest.xml 体积异常过大")
	}

	pkg, splitName, err = parseManifestMeta(data)
	if err != nil {
		return "", "", nil, err
	}

	abis = make([]string, 0, len(abiSet))
	for abi := range abiSet {
		abis = append(abis, abi)
	}
	slices.Sort(abis)
	return pkg, splitName, abis, nil
}

type splitBundleInfo struct {
	apkPaths        []string
	hasOBB          bool
	manifestPackage string
}

func inspectSplitBundle(bundlePath string) (Metadata, error) {
	f, err := os.Open(bundlePath)
	if err != nil {
		return Metadata{}, fmt.Errorf("无法打开分包文件: %w", err)
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return Metadata{}, fmt.Errorf("读取分包文件信息失败: %w", err)
	}
	zr, err := zip.NewReader(f, stat.Size())
	if err != nil {
		return Metadata{}, fmt.Errorf("无法打开分包文件: %w", err)
	}

	abiSet := make(map[string]struct{})
	var (
		pkg                 string
		manifestPackage     string
		manifestChecked     bool
		splitCount          int
		hasOBB              bool
		firstInspectErr     error
		totalInspectedBytes int64
	)

	if len(zr.File) > maxTotalZipEntries {
		return Metadata{}, fmt.Errorf("分包压缩包条目过多（超过 %d 个）", maxTotalZipEntries)
	}

	tmpDir, _ := os.MkdirTemp("", "virdeck-apk-inspect-*")
	if tmpDir != "" {
		defer func() { _ = os.RemoveAll(tmpDir) }()
	}

	for _, entry := range zr.File {
		norm := strings.ReplaceAll(entry.Name, "\\", "/")
		lower := strings.ToLower(norm)
		if strings.HasPrefix(lower, "__macosx/") || strings.Contains(lower, "/__macosx/") {
			continue
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if strings.HasSuffix(lower, ".obb") || strings.Contains(lower, "android/obb/") {
			hasOBB = true
		}
		if !manifestChecked && strings.EqualFold(filepath.Base(norm), "manifest.json") {
			manifestChecked = true
			if rc, openErr := entry.Open(); openErr == nil {
				raw, readErr := io.ReadAll(io.LimitReader(rc, 512*1024))
				_ = rc.Close()
				if readErr == nil {
					var xapkManifest struct {
						PackageName string `json:"package_name"`
					}
					if json.Unmarshal(raw, &xapkManifest) == nil {
						candidate := strings.TrimSpace(xapkManifest.PackageName)
						if isValidAndroidPkg(candidate) {
							manifestPackage = candidate
						}
					}
				}
			}
		}
		if !strings.HasSuffix(lower, ".apk") {
			continue
		}
		splitCount++
		if splitCount > maxSplitBundleEntries {
			return Metadata{}, fmt.Errorf("分包内包含过多 .apk 条目（超过 %d 个）", maxSplitBundleEntries)
		}

		baseName := filepath.Base(norm)
		// Standalone APKs contain combined/fallback payloads; if splits also exist we only need splits for ABI/package.
		var (
			ePkg  string
			eABIs []string
			eErr  error
		)
		if entry.Method == zip.Store && entry.UncompressedSize64 > 0 && entry.UncompressedSize64 <= uint64(maxSplitExtractBytes) {
			if off, offErr := entry.DataOffset(); offErr == nil {
				subSR := io.NewSectionReader(f, off, int64(entry.UncompressedSize64))
				if subZR, subErr := zip.NewReader(subSR, int64(entry.UncompressedSize64)); subErr == nil {
					ePkg, _, eABIs, eErr = inspectZipReaderAPK(subZR)
				}
			}
		}
		if ePkg == "" && len(eABIs) == 0 && eErr == nil && tmpDir != "" {
			// Skip decompressing pure language/density config entries once package is known.
			needDecompress := pkg == "" || isBaseSplitFilename(baseName) || abiTokenFromName(baseName) != "" || (!isDensityConfigName(baseName) && !isLangConfigName(baseName))
			if needDecompress {
				remaining := maxSplitExtractBytes - totalInspectedBytes
				if remaining <= 0 || entry.UncompressedSize64 > uint64(remaining) {
					return Metadata{}, errSplitSizeLimit
				}
				tmpPath := filepath.Join(tmpDir, fmt.Sprintf("%03d_%s", splitCount, sanitizeSplitAPKFilename(baseName)))
				copied, copyErr := copyZipEntryToFile(entry, tmpPath, remaining)
				totalInspectedBytes += copied
				if copyErr != nil {
					if errors.Is(copyErr, errSplitSizeLimit) {
						return Metadata{}, copyErr
					}
					eErr = copyErr
				} else {
					ePkg, _, eABIs, eErr = inspectSingleAPKFull(tmpPath)
					if eErr != nil {
						eABIs = scanAPKABIs(tmpPath)
					}
					_ = os.Remove(tmpPath)
				}
			}
		}
		if eErr != nil && firstInspectErr == nil {
			firstInspectErr = eErr
		}
		if ePkg != "" && (pkg == "" || isBaseSplitFilename(baseName)) {
			pkg = ePkg
		}
		for _, abi := range eABIs {
			abiSet[abi] = struct{}{}
		}
	}

	if splitCount == 0 {
		return Metadata{}, errors.New("分包文件内未找到任何 .apk 安装包")
	}
	if pkg == "" {
		pkg = manifestPackage
	}
	if pkg == "" {
		if firstInspectErr != nil {
			return Metadata{}, firstInspectErr
		}
		return Metadata{}, errors.New("未能在分包中解析到应用包名")
	}

	abis := make([]string, 0, len(abiSet))
	for abi := range abiSet {
		abis = append(abis, abi)
	}
	slices.Sort(abis)

	return Metadata{
		Path:       bundlePath,
		Package:    pkg,
		ABIs:       abis,
		IsSplit:    true,
		SplitCount: splitCount,
		HasOBB:     hasOBB,
	}, nil
}

func copyZipEntryToFile(entry *zip.File, dst string, limit int64) (int64, error) {
	rc, err := entry.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(out, io.LimitReader(rc, limit+1))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return n, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return n, closeErr
	}
	if n > limit {
		_ = os.Remove(dst)
		return n, errSplitSizeLimit
	}
	return n, nil
}

// ExtractSplitAPKs unpacks all .apk splits from a .apks / .xapk bundle into an isolated temp directory.
// The caller must invoke the returned cleanup function once installation completes.
func ExtractSplitAPKs(bundlePath string) (apkPaths []string, cleanup func(), err error) {
	bundlePath = strings.TrimSpace(bundlePath)
	if bundlePath == "" || strings.HasPrefix(bundlePath, "-") {
		return nil, func() {}, fmt.Errorf("无效的分包路径: %q", bundlePath)
	}
	if !IsSplitBundlePath(bundlePath) {
		return nil, func() {}, fmt.Errorf("不支持的分包格式: %s", filepath.Ext(bundlePath))
	}
	tmpDir, err := os.MkdirTemp("", "virdeck-split-*")
	if err != nil {
		return nil, func() {}, fmt.Errorf("创建分包解压目录失败: %w", err)
	}
	clean := func() { _ = os.RemoveAll(tmpDir) }
	bundle, err := extractSplitBundle(bundlePath, tmpDir)
	if err != nil {
		clean()
		return nil, func() {}, err
	}
	return bundle.apkPaths, clean, nil
}

func extractSplitBundle(bundlePath, destDir string) (splitBundleInfo, error) {
	r, err := zip.OpenReader(bundlePath)
	if err != nil {
		return splitBundleInfo{}, fmt.Errorf("无法打开分包文件: %w", err)
	}
	defer r.Close()

	if len(r.File) > maxTotalZipEntries {
		return splitBundleInfo{}, fmt.Errorf("分包压缩包条目过多（超过 %d 个）", maxTotalZipEntries)
	}

	var info splitBundleInfo
	var totalBytes int64
	var manifestChecked bool
	type extractedEntry struct {
		origName string
		destPath string
		isBase   bool
	}
	var extracted []extractedEntry

	for _, f := range r.File {
		norm := strings.ReplaceAll(f.Name, "\\", "/")
		lower := strings.ToLower(norm)
		if strings.HasPrefix(lower, "__macosx/") || strings.Contains(lower, "/__macosx/") {
			continue
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if strings.HasSuffix(lower, ".obb") || strings.Contains(lower, "android/obb/") {
			info.hasOBB = true
		}
		if !manifestChecked && strings.EqualFold(filepath.Base(norm), "manifest.json") {
			manifestChecked = true
			if rc, openErr := f.Open(); openErr == nil {
				raw, readErr := io.ReadAll(io.LimitReader(rc, 512*1024))
				_ = rc.Close()
				if readErr == nil {
					var xapkManifest struct {
						PackageName string `json:"package_name"`
					}
					if json.Unmarshal(raw, &xapkManifest) == nil {
						candidate := strings.TrimSpace(xapkManifest.PackageName)
						if isValidAndroidPkg(candidate) {
							info.manifestPackage = candidate
						}
					}
				}
			}
		}
		if !strings.HasSuffix(lower, ".apk") {
			continue
		}
		if len(extracted) >= maxSplitBundleEntries {
			return splitBundleInfo{}, fmt.Errorf("分包内包含过多 .apk 条目（超过 %d 个）", maxSplitBundleEntries)
		}

		safeBase := sanitizeSplitAPKFilename(filepath.Base(norm))
		if (strings.HasPrefix(lower, "standalones/") || strings.Contains(lower, "/standalones/")) && !strings.Contains(strings.ToLower(safeBase), "standalone") {
			safeBase = "standalone_" + safeBase
		}
		outName := fmt.Sprintf("%03d_%s", len(extracted), safeBase)
		outPath := filepath.Join(destDir, outName)

		rc, err := f.Open()
		if err != nil {
			return splitBundleInfo{}, fmt.Errorf("读取分包条目 %s 失败: %w", f.Name, err)
		}
		outFile, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			_ = rc.Close()
			return splitBundleInfo{}, fmt.Errorf("写出分包文件失败: %w", err)
		}
		remaining := maxSplitExtractBytes - totalBytes
		if remaining <= 0 {
			_ = outFile.Close()
			_ = rc.Close()
			return splitBundleInfo{}, errors.New("分包解压总体积超出 2 GiB 安全限制")
		}
		n, copyErr := io.Copy(outFile, io.LimitReader(rc, remaining+1))
		closeErr := outFile.Close()
		_ = rc.Close()
		if copyErr != nil {
			return splitBundleInfo{}, fmt.Errorf("解压分包条目 %s 失败: %w", f.Name, copyErr)
		}
		if closeErr != nil {
			return splitBundleInfo{}, fmt.Errorf("写入分包条目 %s 失败: %w", f.Name, closeErr)
		}
		totalBytes += n
		if totalBytes > maxSplitExtractBytes {
			return splitBundleInfo{}, errors.New("分包解压总体积超出 2 GiB 安全限制")
		}

		extracted = append(extracted, extractedEntry{
			origName: filepath.Base(norm),
			destPath: outPath,
			isBase:   isBaseSplitFilename(filepath.Base(norm)),
		})
	}

	if len(extracted) == 0 {
		return splitBundleInfo{}, errors.New("分包文件内未找到任何 .apk 安装包")
	}

	sort.SliceStable(extracted, func(i, j int) bool {
		if extracted[i].isBase != extracted[j].isBase {
			return extracted[i].isBase
		}
		return false
	})
	info.apkPaths = make([]string, len(extracted))
	for i, item := range extracted {
		info.apkPaths[i] = item.destPath
	}
	return info, nil
}

func isBaseSplitFilename(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	if lower == "base.apk" || lower == "base-master.apk" {
		return true
	}
	return !strings.HasPrefix(lower, "config.") && !strings.HasPrefix(lower, "split.") && !strings.HasPrefix(lower, "split_") && !strings.HasPrefix(lower, "base-")
}

func sanitizeSplitAPKFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	clean := strings.Trim(b.String(), "._")
	if clean == "" || !strings.HasSuffix(strings.ToLower(clean), ".apk") {
		return "split.apk"
	}
	return clean
}

func scanAPKABIs(apkPath string) []string {
	r, err := zip.OpenReader(apkPath)
	if err != nil {
		return nil
	}
	defer r.Close()
	set := make(map[string]struct{})
	for _, f := range r.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if strings.HasPrefix(name, "lib/") {
			parts := strings.Split(name, "/")
			if len(parts) >= 3 && parts[1] != "" && !f.FileInfo().IsDir() {
				set[parts[1]] = struct{}{}
			}
		}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for abi := range set {
		out = append(out, abi)
	}
	slices.Sort(out)
	return out
}

// SelectSplitAPKsForABIs picks matching split APKs (single base, best ABI/DPI variants, no duplicate split names).
func SelectSplitAPKsForABIs(apkPaths []string, instanceABIs []string, imageABI string, imageCompat catalog.ARMCompat) []string {
	return SelectSplitAPKs(apkPaths, instanceABIs, imageABI, imageCompat, 320)
}

// SelectSplitAPKs selects a consistent mutually-exclusive set of split APKs for the target instance's
// ABI list and screen DPI, avoiding duplicate base variants, conflicting density/ABI splits, or standalone+split mixups.
func SelectSplitAPKs(apkPaths []string, instanceABIs []string, imageABI string, imageCompat catalog.ARMCompat, targetDPI int) []string {
	if len(apkPaths) <= 1 {
		return apkPaths
	}
	if targetDPI <= 0 {
		targetDPI = 320
	}

	type splitItem struct {
		idx          int
		path         string
		baseName     string
		splitName    string
		abis         map[string]struct{}
		dpi          int
		isStandalone bool
		isConfig     bool
	}
	items := make([]splitItem, 0, len(apkPaths))
	hasSplitEntry := false

	for idx, p := range apkPaths {
		baseName := filepath.Base(p)
		_, splitName, manifestABIs, inspectErr := inspectSingleAPKFull(p)
		if inspectErr != nil {
			manifestABIs = scanAPKABIs(p)
		}
		abiMap := make(map[string]struct{}, len(manifestABIs)+1)
		for _, a := range manifestABIs {
			if norm := normalizeABI(a); norm != "" {
				abiMap[norm] = struct{}{}
			}
		}
		if len(abiMap) == 0 {
			if tokABI := abiTokenFromName(splitName); tokABI != "" {
				abiMap[tokABI] = struct{}{}
			} else if tokABI := abiTokenFromName(baseName); tokABI != "" {
				abiMap[tokABI] = struct{}{}
			}
		}
		standalone := strings.Contains(strings.ToLower(baseName), "standalone")
		isCfg := splitName != "" || isConfigSplitFilename(baseName)
		if !standalone && isCfg {
			hasSplitEntry = true
		}
		dpi := densityFromToken(splitName)
		if dpi == 0 {
			dpi = densityFromToken(baseName)
		}
		items = append(items, splitItem{
			idx:          idx,
			path:         p,
			baseName:     baseName,
			splitName:    splitName,
			abis:         abiMap,
			dpi:          dpi,
			isStandalone: standalone,
			isConfig:     isCfg,
		})
	}

	preferred := preferredABIList(instanceABIs, imageABI, imageCompat)

	// Pick best ABI across all non-excluded items that declare at least one ABI.
	bestABI := ""
	for _, cand := range preferred {
		for _, it := range items {
			if hasSplitEntry && it.isStandalone {
				continue
			}
			if _, ok := it.abis[cand]; ok {
				bestABI = cand
				break
			}
		}
		if bestABI != "" {
			break
		}
	}

	// Standalone-only bundles: return a single best standalone APK matching bestABI and closest DPI.
	nonStandaloneCount := 0
	for _, it := range items {
		if !it.isStandalone {
			nonStandaloneCount++
		}
	}
	if nonStandaloneCount == 0 {
		bestIdx := 0
		bestScore := -1
		for i, it := range items {
			score := 0
			if bestABI != "" {
				if _, ok := it.abis[bestABI]; ok {
					score += 10000
				} else if len(it.abis) > 0 {
					continue
				}
			}
			if it.dpi > 0 {
				diff := it.dpi - targetDPI
				if diff < 0 {
					diff = -diff
				}
				score += 1000 - diff
			}
			if bestScore < 0 || score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}
		return []string{items[bestIdx].path}
	}

	// Pick single best base APK (!isStandalone && !isConfig).
	bestBaseIdx := -1
	bestBaseRank := -1
	for i, it := range items {
		if (hasSplitEntry && it.isStandalone) || it.isConfig {
			continue
		}
		if bestABI != "" && len(it.abis) > 0 {
			if _, ok := it.abis[bestABI]; !ok {
				continue
			}
		}
		rank := 10
		if bestABI != "" && len(it.abis) > 0 {
			if _, ok := it.abis[bestABI]; ok {
				rank += 100
			}
		}
		lower := stripNumericPrefix(strings.ToLower(it.baseName))
		if lower == "base.apk" || lower == "base-master.apk" {
			rank += 20
		}
		if rank > bestBaseRank {
			bestBaseRank = rank
			bestBaseIdx = i
		}
	}

	// Pick single best DPI split closest to targetDPI.
	bestDPIIdx := -1
	bestDPIDiff := 1 << 30
	bestDPIVal := 0
	for i, it := range items {
		if (hasSplitEntry && it.isStandalone) || !it.isConfig || it.dpi <= 0 {
			continue
		}
		if bestABI != "" && len(it.abis) > 0 {
			if _, ok := it.abis[bestABI]; !ok {
				continue
			}
		}
		diff := it.dpi - targetDPI
		if diff < 0 {
			diff = -diff
		}
		if diff < bestDPIDiff || (diff == bestDPIDiff && it.dpi >= targetDPI && bestDPIVal < targetDPI) {
			bestDPIDiff = diff
			bestDPIVal = it.dpi
			bestDPIIdx = i
		}
	}

	seenSplits := make(map[string]struct{})
	out := make([]string, 0, len(items))
	if bestBaseIdx >= 0 {
		out = append(out, items[bestBaseIdx].path)
	}
	for i, it := range items {
		if (hasSplitEntry && it.isStandalone) || !it.isConfig {
			continue
		}
		if bestABI != "" && len(it.abis) > 0 {
			if _, ok := it.abis[bestABI]; !ok {
				continue
			}
		}
		if it.dpi > 0 && i != bestDPIIdx {
			continue
		}
		if it.splitName != "" {
			if _, dup := seenSplits[it.splitName]; dup {
				continue
			}
			seenSplits[it.splitName] = struct{}{}
		}
		out = append(out, it.path)
	}
	if len(out) == 0 {
		return apkPaths
	}
	return out
}

func stripNumericPrefix(lowerName string) string {
	// ExtractSplitAPKs writes "%03d_<name>".
	if len(lowerName) > 4 && lowerName[3] == '_' &&
		lowerName[0] >= '0' && lowerName[0] <= '9' &&
		lowerName[1] >= '0' && lowerName[1] <= '9' &&
		lowerName[2] >= '0' && lowerName[2] <= '9' {
		return lowerName[4:]
	}
	return lowerName
}

func isConfigSplitFilename(baseName string) bool {
	lower := stripNumericPrefix(strings.ToLower(strings.TrimSpace(baseName)))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "config.") || strings.HasPrefix(lower, "split.") || strings.HasPrefix(lower, "split_") {
		return true
	}
	if strings.HasPrefix(lower, "base-") && lower != "base-master.apk" && !strings.HasPrefix(lower, "base-master_") {
		return true
	}
	return abiTokenFromName(lower) != "" || densityFromToken(lower) > 0
}

func abiTokenFromName(s string) string {
	lower := strings.ToLower(s)
	switch {
	case strings.Contains(lower, "arm64_v8a") || strings.Contains(lower, "arm64-v8a"):
		return "arm64-v8a"
	case strings.Contains(lower, "armeabi_v7a") || strings.Contains(lower, "armeabi-v7a"):
		return "armeabi-v7a"
	case strings.Contains(lower, "x86_64"):
		return "x86_64"
	case strings.Contains(lower, "x86"):
		return "x86"
	case strings.Contains(lower, "armeabi"):
		return "armeabi"
	default:
		return ""
	}
}

func densityFromToken(s string) int {
	lower := strings.ToLower(s)
	switch {
	case strings.Contains(lower, "xxxhdpi"):
		return 640
	case strings.Contains(lower, "xxhdpi"):
		return 480
	case strings.Contains(lower, "xhdpi"):
		return 320
	case strings.Contains(lower, "tvdpi"):
		return 213
	case strings.Contains(lower, "hdpi"):
		return 240
	case strings.Contains(lower, "mdpi"):
		return 160
	case strings.Contains(lower, "ldpi"):
		return 120
	default:
		return 0
	}
}

func isDensityConfigName(baseName string) bool {
	return densityFromToken(baseName) > 0 || strings.Contains(strings.ToLower(baseName), "nodpi") || strings.Contains(strings.ToLower(baseName), "anydpi")
}

func isLangConfigName(baseName string) bool {
	lower := strings.ToLower(strings.TrimSuffix(baseName, filepath.Ext(baseName)))
	return strings.HasPrefix(lower, "config.") && len(lower) >= 9 && len(lower) <= 13 && abiTokenFromName(lower) == "" && !isDensityConfigName(lower)
}

func preferredABIList(instanceABIs []string, imageABI string, imageCompat catalog.ARMCompat) []string {
	var out []string
	seen := make(map[string]struct{})
	add := func(s string) {
		norm := normalizeABI(s)
		if norm == "" {
			return
		}
		if _, ok := seen[norm]; ok {
			return
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
	}
	if len(instanceABIs) > 0 {
		for _, abi := range instanceABIs {
			add(abi)
		}
		return out
	}
	switch normalizeABI(imageABI) {
	case "x86_64":
		add("x86_64")
		add("x86")
	case "x86":
		add("x86")
	case "arm64-v8a":
		add("arm64-v8a")
		add("armeabi-v7a")
		add("armeabi")
	case "armeabi-v7a":
		add("armeabi-v7a")
		add("armeabi")
	default:
		add(imageABI)
	}
	if imageCompat == catalog.ARMCompatNative || imageCompat == catalog.ARMCompatTranslation {
		add("arm64-v8a")
		add("armeabi-v7a")
		add("armeabi")
	}
	return out
}

// CheckARMCompatibility compares APK native ABIs against runtime instance ABIs (or image fallback rules).
func CheckARMCompatibility(apkABIs, instanceABIs []string, imageABI string, imageCompat catalog.ARMCompat) (bool, string) {
	if len(apkABIs) == 0 {
		return true, ""
	}

	if len(instanceABIs) > 0 {
		supported := make(map[string]struct{}, len(instanceABIs))
		for _, abi := range instanceABIs {
			if norm := normalizeABI(abi); norm != "" {
				supported[norm] = struct{}{}
			}
		}
		for _, need := range apkABIs {
			if _, ok := supported[normalizeABI(need)]; ok {
				return true, ""
			}
		}
		return false, fmt.Sprintf("该 APK 仅包含 %s 原生库，而目标实例运行态支持 %s，可能无法安装或闪退",
			strings.Join(apkABIs, "/"), strings.Join(instanceABIs, "/"))
	}

	inferred := make(map[string]struct{})
	switch normalizeABI(imageABI) {
	case "x86_64":
		inferred["x86_64"] = struct{}{}
		inferred["x86"] = struct{}{}
	case "x86":
		inferred["x86"] = struct{}{}
	case "arm64-v8a":
		inferred["arm64-v8a"] = struct{}{}
		inferred["armeabi-v7a"] = struct{}{}
		inferred["armeabi"] = struct{}{}
	case "armeabi-v7a":
		inferred["armeabi-v7a"] = struct{}{}
		inferred["armeabi"] = struct{}{}
	default:
		if norm := normalizeABI(imageABI); norm != "" {
			inferred[norm] = struct{}{}
		}
	}
	if imageCompat == catalog.ARMCompatNative || imageCompat == catalog.ARMCompatTranslation {
		inferred["arm64-v8a"] = struct{}{}
		inferred["armeabi-v7a"] = struct{}{}
		inferred["armeabi"] = struct{}{}
	}

	for _, need := range apkABIs {
		if _, ok := inferred[normalizeABI(need)]; ok {
			return true, ""
		}
	}
	return false, fmt.Sprintf("该 APK 仅含 %s 原生库，目标镜像不支持 ARM 转译，安装或运行可能失败", strings.Join(apkABIs, "/"))
}

func normalizeABI(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func parseManifestPackage(data []byte) (string, error) {
	pkg, _, err := parseManifestMeta(data)
	return pkg, err
}

func parseManifestMeta(data []byte) (pkg, splitName string, err error) {
	if len(data) < 8 {
		return "", "", errors.New("AndroidManifest.xml 格式无效（数据过短）")
	}
	if binary.LittleEndian.Uint16(data[0:2]) != resXMLType {
		return "", "", errors.New("AndroidManifest.xml 不是有效的二进制 AXML 格式")
	}
	rootHeaderSize := int(binary.LittleEndian.Uint16(data[2:4]))
	if rootHeaderSize < 8 || rootHeaderSize > len(data) {
		return "", "", errors.New("AndroidManifest.xml 头部尺寸无效")
	}

	var pool []string
	offset := rootHeaderSize
	for offset+8 <= len(data) {
		chunkType := binary.LittleEndian.Uint16(data[offset : offset+2])
		chunkHeaderSize := int(binary.LittleEndian.Uint16(data[offset+2 : offset+4]))
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if chunkHeaderSize < 8 || chunkSize < chunkHeaderSize || chunkSize > len(data)-offset {
			return "", "", errors.New("AndroidManifest.xml 数据块损坏")
		}

		switch chunkType {
		case resStringPoolType:
			parsed, err := parseStringPool(data[offset : offset+chunkSize])
			if err != nil {
				return "", "", err
			}
			pool = parsed
		case resXMLStartElementType:
			parsedPkg, parsedSplit, matched, err := parseManifestStartElement(data[offset:offset+chunkSize], chunkHeaderSize, pool)
			if err != nil {
				return "", "", err
			}
			if matched {
				if parsedPkg == "" {
					return "", "", errors.New("AndroidManifest.xml 中 manifest@package 为空")
				}
				return parsedPkg, parsedSplit, nil
			}
		}

		offset += chunkSize
	}

	return "", "", errors.New("未在 AndroidManifest.xml 中找到 manifest@package")
}

func parseStringPool(chunk []byte) ([]string, error) {
	if len(chunk) < 28 {
		return nil, errors.New("AXML 字符串池头部损坏")
	}
	headerSize := int(binary.LittleEndian.Uint16(chunk[2:4]))
	stringCount := int(binary.LittleEndian.Uint32(chunk[8:12]))
	flags := binary.LittleEndian.Uint32(chunk[16:20])
	stringsStart := int(binary.LittleEndian.Uint32(chunk[20:24]))

	if headerSize < 28 || stringCount < 0 || stringCount > maxStringPoolEntryCount {
		return nil, errors.New("AXML 字符串池数量无效")
	}
	offsetsEnd := headerSize + stringCount*4
	if offsetsEnd > len(chunk) || stringsStart < offsetsEnd || stringsStart > len(chunk) {
		return nil, errors.New("AXML 字符串池偏移越界")
	}

	isUTF8 := (flags & stringPoolUTF8Flag) != 0
	out := make([]string, stringCount)
	decoded := make(map[int]string)
	decodedBytes := 0
	for i := 0; i < stringCount; i++ {
		relOff := int(binary.LittleEndian.Uint32(chunk[headerSize+i*4 : headerSize+i*4+4]))
		pos := stringsStart + relOff
		if pos < stringsStart || pos >= len(chunk) {
			return nil, errors.New("AXML 字符串偏移越界")
		}
		if cached, ok := decoded[pos]; ok {
			out[i] = cached
			continue
		}
		var (
			s   string
			err error
		)
		if isUTF8 {
			s, err = decodePoolUTF8(chunk, pos)
		} else {
			s, err = decodePoolUTF16(chunk, pos)
		}
		if err != nil {
			return nil, err
		}
		decodedBytes += len(s)
		if decodedBytes > maxDecodedPoolBytes {
			return nil, errors.New("AXML 字符串池解码体积超出安全上限")
		}
		decoded[pos] = s
		out[i] = s
	}
	return out, nil
}

func decodePoolUTF8(chunk []byte, pos int) (string, error) {
	// Skip UTF-16 code-unit length (1 or 2 bytes)
	if pos >= len(chunk) {
		return "", errors.New("AXML UTF-8 字符串长度越界")
	}
	if chunk[pos]&0x80 != 0 {
		pos += 2
	} else {
		pos++
	}
	if pos >= len(chunk) {
		return "", errors.New("AXML UTF-8 字节长度越界")
	}
	var byteLen int
	if chunk[pos]&0x80 != 0 {
		if pos+1 >= len(chunk) {
			return "", errors.New("AXML UTF-8 字节长度截断")
		}
		byteLen = int(chunk[pos]&0x7f)<<8 | int(chunk[pos+1])
		pos += 2
	} else {
		byteLen = int(chunk[pos])
		pos++
	}
	if pos+byteLen > len(chunk) {
		return "", errors.New("AXML UTF-8 字符串内容越界")
	}
	return string(chunk[pos : pos+byteLen]), nil
}

func decodePoolUTF16(chunk []byte, pos int) (string, error) {
	if pos+2 > len(chunk) {
		return "", errors.New("AXML UTF-16 字符串长度越界")
	}
	u0 := binary.LittleEndian.Uint16(chunk[pos : pos+2])
	pos += 2
	charLen := int(u0)
	if u0&0x8000 != 0 {
		if pos+2 > len(chunk) {
			return "", errors.New("AXML UTF-16 字符串长度截断")
		}
		u1 := binary.LittleEndian.Uint16(chunk[pos : pos+2])
		pos += 2
		charLen = int(uint32(u0&0x7fff)<<16 | uint32(u1))
	}
	if pos+charLen*2 > len(chunk) {
		return "", errors.New("AXML UTF-16 字符串内容越界")
	}
	units := make([]uint16, charLen)
	for i := 0; i < charLen; i++ {
		units[i] = binary.LittleEndian.Uint16(chunk[pos+i*2 : pos+i*2+2])
	}
	return string(utf16.Decode(units)), nil
}

func parseManifestStartElement(chunk []byte, headerSize int, pool []string) (pkg, splitName string, isManifest bool, err error) {
	if headerSize+20 > len(chunk) {
		return "", "", false, errors.New("AXML 元素块损坏")
	}
	ext := chunk[headerSize:]
	nameIdx := int32(binary.LittleEndian.Uint32(ext[4:8]))
	if nameIdx < 0 || int(nameIdx) >= len(pool) || pool[nameIdx] != "manifest" {
		return "", "", false, nil
	}

	attrStart := int(binary.LittleEndian.Uint16(ext[8:10]))
	attrSize := int(binary.LittleEndian.Uint16(ext[10:12]))
	attrCount := int(binary.LittleEndian.Uint16(ext[12:14]))
	if attrCount == 0 {
		return "", "", true, nil
	}
	if attrSize < 20 || attrStart < 20 {
		return "", "", true, errors.New("AXML 属性布局无效")
	}
	if attrStart+attrCount*attrSize > len(ext) {
		return "", "", true, errors.New("AXML 属性列表越界")
	}

	for i := 0; i < attrCount; i++ {
		attr := ext[attrStart+i*attrSize : attrStart+i*attrSize+20]
		attrNameIdx := int32(binary.LittleEndian.Uint32(attr[4:8]))
		if attrNameIdx < 0 || int(attrNameIdx) >= len(pool) {
			continue
		}
		attrName := pool[attrNameIdx]
		if attrName != "package" && attrName != "split" {
			continue
		}
		var val string
		rawValIdx := int32(binary.LittleEndian.Uint32(attr[8:12]))
		if rawValIdx >= 0 && int(rawValIdx) < len(pool) {
			val = strings.TrimSpace(pool[rawValIdx])
		} else if attr[15] == typedValueStringType {
			valData := binary.LittleEndian.Uint32(attr[16:20])
			if uint64(valData) < uint64(len(pool)) {
				val = strings.TrimSpace(pool[valData])
			}
		}
		switch attrName {
		case "package":
			pkg = val
		case "split":
			splitName = val
		}
	}
	return pkg, splitName, true, nil
}

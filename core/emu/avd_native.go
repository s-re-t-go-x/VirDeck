package emu

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"virdeck/config"
)

// ResolutionPreset defines a screen resolution and density preset for an AVD.
type ResolutionPreset struct {
	Width   int
	Height  int
	Density int
}

// LookupResolution returns the width, height, and DPI for "720p" (default, low-memory) or "1080p".
func LookupResolution(preset string) ResolutionPreset {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "1080p", "1080x1920", "fhd":
		return ResolutionPreset{Width: 1080, Height: 1920, Density: 420}
	default:
		return ResolutionPreset{Width: 720, Height: 1280, Density: 320}
	}
}

// CreateAVDNative creates a clean AVD instance directly in pure Go (without avdmanager.bat or JDK/Java)
// by generating <avdHome>/<name>.ini and <avdHome>/<name>.avd/config.ini, plus ensuring package.xml exists.
func (c *Controller) CreateAVDNative(name, systemImage string, memoryMB, cores int, resolution string) error {
	if c.avdHome == "" {
		return fmt.Errorf("未配置 AVD 存储目录 (ANDROID_AVD_HOME)")
	}
	if c.sdkRoot == "" {
		return fmt.Errorf("未配置 Android SDK 根目录 (ANDROID_SDK_ROOT)")
	}
	if !isSafePathSegment(name) {
		return fmt.Errorf("无效的 AVD 名称: %s", name)
	}
	apiFolder, tagDir, abi, ok := splitSystemImageID(systemImage)
	if !ok {
		return fmt.Errorf("系统镜像包格式无效 (%s)，应为 system-images;android-<API>;<tag>;<abi>", systemImage)
	}

	sysImgDir := filepath.Join(c.sdkRoot, "system-images", apiFolder, tagDir, abi)
	if !dirExists(sysImgDir) {
		return fmt.Errorf("本地未找到已安装的系统镜像目录: %s", sysImgDir)
	}

	// Ensure package.xml and platforms/<apiFolder> placeholder exist so emulator.exe accepts the image.
	if err := EnsureSystemImageMetadata(c.sdkRoot, systemImage); err != nil {
		return err
	}

	avdDir := filepath.Join(c.avdHome, name+".avd")
	_ = os.RemoveAll(avdDir)
	if err := os.MkdirAll(avdDir, 0o755); err != nil {
		return fmt.Errorf("创建 AVD 目录失败: %w", err)
	}

	props := readKeyValueFile(filepath.Join(sysImgDir, "source.properties"))
	tagIDs := strings.TrimSpace(props["SystemImage.TagId"])
	if tagIDs == "" {
		tagIDs = tagDir
	}
	primaryTagID := strings.TrimSpace(strings.Split(tagIDs, ",")[0])
	tagDisplays := strings.TrimSpace(props["SystemImage.TagDisplay"])
	if tagDisplays == "" {
		tagDisplays = defaultTagDisplay(primaryTagID)
	}
	primaryTagDisplay := strings.TrimSpace(strings.Split(tagDisplays, ",")[0])

	playStoreEnabled := "false"
	if strings.Contains(strings.ToLower(tagIDs), "playstore") || strings.Contains(strings.ToLower(tagDir), "playstore") {
		playStoreEnabled = "true"
	}

	if memoryMB <= 0 {
		memoryMB = config.Values.Create.MemoryMB
	}
	if cores <= 0 {
		cores = config.Values.Create.Cores
	}
	res := LookupResolution(resolution)

	// Relative sysdir with trailing backslash as expected by emulator.exe on Windows.
	relSysDir := filepath.Join("system-images", apiFolder, tagDir, abi) + string(os.PathSeparator)

	var cfg strings.Builder
	w := func(k, v string) {
		cfg.WriteString(cleanIniLine(k))
		cfg.WriteString("=")
		cfg.WriteString(cleanIniLine(v))
		cfg.WriteString("\n")
	}

	w("AvdId", name)
	w("PlayStore.enabled", playStoreEnabled)
	w("abi.type", abi)
	w("avd.ini.displayname", name)
	w("avd.ini.encoding", "UTF-8")
	w("disk.dataPartition.size", "6G")
	w("fastboot.chosenSnapshotFile", "")
	w("fastboot.forceChosenSnapshotBoot", "no")
	w("fastboot.forceColdBoot", "yes")
	w("fastboot.forceFastBoot", "no")
	w("hw.accelerometer", "yes")
	w("hw.arc", "false")
	w("hw.audioInput", "yes")
	w("hw.battery", "yes")
	w("hw.camera.back", "virtualscene")
	w("hw.camera.front", "emulated")
	cpuArch := abi
	if abi == "arm64-v8a" {
		cpuArch = "arm64"
	}
	w("hw.cpu.arch", cpuArch)
	w("hw.cpu.ncore", strconv.Itoa(cores))
	w("hw.dPad", "no")
	w("hw.device.manufacturer", "Google")
	w("hw.device.name", "pixel_5")
	w("hw.gps", "yes")
	w("hw.gpu.enabled", "yes")
	w("hw.gpu.mode", "auto")
	w("hw.gyroscope", "yes")
	w("hw.initialOrientation", "portrait")
	w("hw.keyboard", "yes")
	w("hw.lcd.density", strconv.Itoa(res.Density))
	w("hw.lcd.height", strconv.Itoa(res.Height))
	w("hw.lcd.width", strconv.Itoa(res.Width))
	w("hw.mainKeys", "no")
	w("hw.ramSize", strconv.Itoa(memoryMB))
	w("hw.sdCard", "yes")
	w("hw.sensors.light", "yes")
	w("hw.sensors.magnetic_field", "yes")
	w("hw.sensors.orientation", "yes")
	w("hw.sensors.pressure", "yes")
	w("hw.sensors.proximity", "yes")
	w("hw.trackBall", "no")
	w("image.sysdir.1", relSysDir)
	w("runtime.network.latency", "none")
	w("runtime.network.speed", "full")
	w("sdcard.size", "512M")
	w("showDeviceFrame", "no")
	w("tag.display", primaryTagDisplay)
	if strings.Contains(tagDisplays, ",") {
		w("tag.displaynames", tagDisplays)
	}
	w("tag.id", primaryTagID)
	if strings.Contains(tagIDs, ",") {
		w("tag.ids", tagIDs)
	}
	w("target", apiFolder)
	w("vm.heapSize", "256")

	cfgPath := filepath.Join(avdDir, "config.ini")
	if err := os.WriteFile(cfgPath, []byte(cfg.String()), 0o644); err != nil {
		return fmt.Errorf("写入 config.ini 失败: %w", err)
	}

	iniPath := filepath.Join(c.avdHome, name+".ini")
	iniContent := fmt.Sprintf("avd.ini.encoding=UTF-8\npath=%s\npath.rel=%s\ntarget=%s\n", avdDir, filepath.Join("avd", name+".avd"), apiFolder)
	if err := os.WriteFile(iniPath, []byte(iniContent), 0o644); err != nil {
		return fmt.Errorf("写入 %s.ini 失败: %w", name, err)
	}
	return nil
}

// EnsureSystemImageMetadata generates a valid Android SDK package.xml inside the system image directory
// (if missing) and creates the corresponding platforms/<apiFolder> and platform-tools placeholder directories
// required by emulator.exe's path_is_valid_sdk_root() check.
func EnsureSystemImageMetadata(sdkRoot, systemImage string) error {
	apiFolder, tagDir, abi, ok := splitSystemImageID(systemImage)
	if !ok {
		return fmt.Errorf("无效的镜像包 ID: %s", systemImage)
	}
	sysImgDir := filepath.Join(sdkRoot, "system-images", apiFolder, tagDir, abi)
	if err := os.MkdirAll(sysImgDir, 0o755); err != nil {
		return err
	}

	props := readKeyValueFile(filepath.Join(sysImgDir, "source.properties"))
	apiLevel := cleanIniLine(strings.TrimSpace(props["AndroidVersion.ApiLevel"]))
	if apiLevel == "" {
		apiLevel = strings.TrimPrefix(apiFolder, "android-")
	}
	revMajor := "1"
	if r := cleanIniLine(strings.TrimSpace(props["Pkg.Revision"])); r != "" {
		revMajor = strings.Split(r, ".")[0]
	}
	tagIDs := cleanIniLine(strings.TrimSpace(props["SystemImage.TagId"]))
	if tagIDs == "" {
		tagIDs = tagDir
	}
	tagDisplays := cleanIniLine(strings.TrimSpace(props["SystemImage.TagDisplay"]))
	if tagDisplays == "" {
		tagDisplays = defaultTagDisplay(tagDir)
	}
	vendorID := cleanIniLine(strings.TrimSpace(props["Addon.VendorId"]))
	if vendorID == "" {
		vendorID = "google"
	}
	vendorDisplay := cleanIniLine(strings.TrimSpace(props["Addon.VendorDisplay"]))
	if vendorDisplay == "" {
		vendorDisplay = "Google Inc."
	}
	desc := cleanIniLine(strings.TrimSpace(props["Pkg.Desc"]))
	if desc == "" {
		if strings.HasPrefix(abi, "arm") {
			desc = fmt.Sprintf("ARM %s System Image (API %s)", abi, apiLevel)
		} else {
			desc = fmt.Sprintf("Google Play Intel %s Atom System Image (API %s)", abi, apiLevel)
		}
	}

	// Ensure source.properties exists if the zip didn't have one.
	srcPropPath := filepath.Join(sysImgDir, "source.properties")
	if !fileExists(srcPropPath) {
		srcContent := fmt.Sprintf("Pkg.Desc=%s\nPkg.Revision=%s\nAndroidVersion.ApiLevel=%s\nSystemImage.Abi=%s\nSystemImage.TagId=%s\nSystemImage.TagDisplay=%s\nSystemImage.GpuSupport=true\nAddon.VendorId=%s\nAddon.VendorDisplay=%s\n",
			desc, revMajor, apiLevel, abi, tagIDs, tagDisplays, vendorID, vendorDisplay)
		_ = os.WriteFile(srcPropPath, []byte(srcContent), 0o644)
	}

	// Generate package.xml if not already present.
	pkgXMLPath := filepath.Join(sysImgDir, "package.xml")
	if !fileExists(pkgXMLPath) {
		idList := strings.Split(tagIDs, ",")
		dispList := strings.Split(tagDisplays, ",")
		var tagXML strings.Builder
		for i, rawID := range idList {
			tid := strings.TrimSpace(rawID)
			if tid == "" {
				continue
			}
			tdisp := tid
			if i < len(dispList) && strings.TrimSpace(dispList[i]) != "" {
				tdisp = strings.TrimSpace(dispList[i])
			}
			tagXML.WriteString(fmt.Sprintf("            <tag>\n                <id>%s</id>\n                <display>%s</display>\n            </tag>\n", xmlEscape(tid), xmlEscape(tdisp)))
		}

		extXML := ""
		if ext := strings.TrimSpace(props["AndroidVersion.ExtensionLevel"]); ext != "" {
			baseExt := strings.TrimSpace(props["AndroidVersion.IsBaseSdk"])
			if baseExt == "" {
				baseExt = "true"
			}
			extXML = fmt.Sprintf("            <extension-level>%s</extension-level>\n            <base-extension>%s</base-extension>\n", xmlEscape(ext), xmlEscape(baseExt))
		}

		canonicalPkgID := fmt.Sprintf("system-images;%s;%s;%s", apiFolder, tagDir, abi)
		xmlContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<ns2:repository xmlns:ns2="http://schemas.android.com/repository/android/common/02" xmlns:ns12="http://schemas.android.com/sdk/android/repo/sys-img2/04" xmlns:ns13="http://schemas.android.com/sdk/android/repo/sys-img2/03" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
    <license id="android-sdk-license" type="text">Android Software Development Kit License Agreement</license>
    <localPackage path="%s" obsolete="false">
        <type-details xsi:type="ns12:sysImgDetailsType">
            <api-level>%s</api-level>
%s%s            <vendor>
                <id>%s</id>
                <display>%s</display>
            </vendor>
            <abi>%s</abi>
            <abis>%s</abis>
        </type-details>
        <revision>
            <major>%s</major>
        </revision>
        <display-name>%s</display-name>
        <uses-license ref="android-sdk-license"/>
    </localPackage>
</ns2:repository>
`, xmlEscape(canonicalPkgID), xmlEscape(apiLevel), extXML, tagXML.String(), xmlEscape(vendorID), xmlEscape(vendorDisplay), xmlEscape(abi), xmlEscape(abi), xmlEscape(revMajor), xmlEscape(desc))

		if err := os.WriteFile(pkgXMLPath, []byte(xmlContent), 0o644); err != nil {
			return fmt.Errorf("生成 package.xml 失败: %w", err)
		}
	}

	// Ensure platforms/<apiFolder>/build.prop and platform-tools/ placeholder directories exist
	// because emulator.exe's path_is_valid_sdk_root() requires BOTH platforms/ and platform-tools/
	// to exist inside ANDROID_SDK_ROOT!
	platformDir := filepath.Join(sdkRoot, "platforms", apiFolder)
	if err := os.MkdirAll(platformDir, 0o755); err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Join(sdkRoot, "platform-tools"), 0o755)
	buildProp := filepath.Join(platformDir, "build.prop")
	if !fileExists(buildProp) {
		_ = os.WriteFile(buildProp, []byte(fmt.Sprintf("ro.build.version.sdk=%s\n", apiLevel)), 0o644)
	}
	return nil
}

// HealAVDIniPaths scans <avdHome>/*.ini and rewrites `path=` to the current absolute path of <avdHome>/<Name>.avd
// so moving the VirDeck folder across drives/directories never breaks existing instances.
func (c *Controller) HealAVDIniPaths() error {
	if c.avdHome == "" || !dirExists(c.avdHome) {
		return nil
	}
	entries, err := os.ReadDir(c.avdHome)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".ini") {
			continue
		}
		iniPath := filepath.Join(c.avdHome, e.Name())
		baseName := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		expectedAVDDir := filepath.Join(c.avdHome, baseName+".avd")
		if !dirExists(expectedAVDDir) {
			continue
		}
		data, err := os.ReadFile(iniPath)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		changed := false
		for i, line := range lines {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch strings.TrimSpace(k) {
			case "path":
				if strings.TrimSpace(v) != expectedAVDDir {
					lines[i] = "path=" + expectedAVDDir
					changed = true
				}
			case "path.rel":
				rel := filepath.Join("avd", baseName+".avd")
				if strings.TrimSpace(v) != rel {
					lines[i] = "path.rel=" + rel
					changed = true
				}
			}
		}
		if changed {
			_ = os.WriteFile(iniPath, []byte(strings.Join(lines, "\n")), 0o644)
		}
	}
	return nil
}

// ValidateDataDir checks path compatibility and write access. The legacy ASCII
// restriction applies to Windows emulator paths; Unix accepts UTF-8 paths.
func ValidateDataDir(dataDir string) (asciiOK bool, writableOK bool, warningMsg string) {
	return validateDataDirForHost(dataDir, runtime.GOOS)
}

func validateDataDirForHost(dataDir, host string) (asciiOK bool, writableOK bool, warningMsg string) {
	asciiOK = true
	for i := 0; i < len(dataDir); i++ {
		if host == "windows" && dataDir[i] >= 0x80 {
			asciiOK = false
			break
		}
	}
	writableOK = false
	if err := os.MkdirAll(dataDir, 0o755); err == nil {
		probe := filepath.Join(dataDir, ".write_probe")
		if err := os.WriteFile(probe, []byte("ok"), 0o644); err == nil {
			_ = os.Remove(probe)
			writableOK = true
		}
	}
	var warns []string
	if !asciiOK {
		warns = append(warns, fmt.Sprintf("当前路径 %q 含有中文或非 ASCII 字符，Android Emulator 可能启动失败，建议将程序文件夹移至纯英文路径（如 D:\\VirDeck）", dataDir))
	}
	if !writableOK {
		warns = append(warns, fmt.Sprintf("当前目录 %q 无写入权限，请将程序移至普通可写目录或以管理员身份运行", dataDir))
	}
	return asciiOK, writableOK, strings.Join(warns, "；")
}

func readKeyValueFile(path string) map[string]string {
	out := make(map[string]string)
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func defaultTagDisplay(tag string) string {
	switch tag {
	case "google_apis_playstore":
		return "Google Play"
	case "google_apis":
		return "Google APIs"
	default:
		return "Android System Image"
	}
}

func splitSystemImageID(systemImage string) (apiFolder, tagDir, abi string, ok bool) {
	parts := strings.Split(strings.TrimSpace(systemImage), ";")
	if len(parts) != 4 || parts[0] != "system-images" {
		return "", "", "", false
	}
	if !isSafePathSegment(parts[1]) || !isSafePathSegment(parts[2]) || !isSafePathSegment(parts[3]) {
		return "", "", "", false
	}
	return parts[1], parts[2], parts[3], true
}

func isSafePathSegment(seg string) bool {
	if seg == "" || seg == "." || seg == ".." {
		return false
	}
	if strings.ContainsAny(seg, `/\:*?"<>|`+"\x00\r\n") {
		return false
	}
	return true
}

func cleanIniLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

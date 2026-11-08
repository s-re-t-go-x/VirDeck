package mygonative

import (
	"fmt"
	"strings"

	"virdeck/core/service"
)

// envCheck is one line of the first-run environment guide.
type envCheck struct {
	name     string
	ok       bool
	blocking bool   // true: a core feature cannot work until fixed; false: an optional feature is limited
	detail   string // path or status summary
	impact   string // what is affected while it is missing
	fix      string // how to fix it
	toolKey  string // "emulator" | "adb" | "scrcpy" | "" (when downloadable/importable via VirDeck)
}

func buildEnvChecks(env service.EnvStatus) []envCheck {
	tool := func(name, path, impact, fix string, blocking bool, toolKey string) envCheck {
		c := envCheck{
			name:     name,
			ok:       strings.TrimSpace(path) != "",
			blocking: blocking,
			detail:   path,
			toolKey:  toolKey,
		}
		if !c.ok {
			c.detail = "未检测到"
			c.impact, c.fix = impact, fix
		}
		return c
	}

	checks := []envCheck{
		tool("Android SDK 目录", env.SDKRoot,
			"找不到 emulator、adb 等运行工具，实例无法启动和管理。",
			"点击下方基础组件的「一键下载」将自动在 VirDeckData/sdk 初始化免安装 SDK 目录。", true, ""),
		tool("emulator（模拟器）", env.EmulatorPath,
			"无法启动任何实例。",
			`点击右侧「一键下载」自动获取官方模拟器，或点击「导入 .zip」离线安装。`, true, "emulator"),
		tool("adb", env.ADBPath,
			"无法判断实例是否启动完成，也无法安装 APK 或执行批量命令。",
			`点击右侧「一键下载」自动获取官方 platform-tools，或点击「导入 .zip」离线安装。`, true, "adb"),
		tool("scrcpy（投屏）", env.ScrcpyPath,
			"无法打开实例画面，其他功能不受影响。",
			"点击右侧「一键下载」自动获取 scrcpy-win64，或点击「导入 .zip」解压至 VirDeckData/scrcpy。", false, "scrcpy"),
	}

	// Path & permission self-check (ASCII path & writable VirDeckData).
	pathOK := (env.DataDir != "" && env.PathASCIIOK && env.PathWritableOK) ||
		(env.DataDir == "" && strings.TrimSpace(env.SDKRoot) != "" && env.PathWarning == "")
	pathDetail := env.DataDir
	if strings.TrimSpace(pathDetail) == "" {
		pathDetail = env.AVDHome
	}
	pathCheck := envCheck{
		name:     "数据目录与权限",
		ok:       pathOK,
		blocking: true,
		detail:   pathDetail,
	}
	if !pathCheck.ok {
		if env.PathWarning != "" {
			pathCheck.detail = "路径异常"
			pathCheck.impact = env.PathWarning
		} else {
			pathCheck.detail = "未就绪"
			pathCheck.impact = "Google 官方模拟器底层不支持含中文/非 ASCII 字符或无写入权限的目录。"
		}
		pathCheck.fix = "请将 VirDeck 放置在纯英文/数字且有读写权限的路径下（如 D:\\VirDeck）。"
	}
	checks = append(checks, pathCheck)

	accel := envCheck{name: "硬件加速 (WHPX)", ok: env.AccelOK, blocking: true, detail: env.AccelMessage}
	if strings.TrimSpace(accel.detail) == "" {
		accel.detail = "已启用"
		if !accel.ok {
			accel.detail = "未检测到"
		}
	}
	if !accel.ok {
		accel.impact = "模拟器会极度卡顿，甚至无法启动。"
		accel.fix = "在「启用或关闭 Windows 功能」中勾选「Windows 虚拟机监控程序平台」，确定后重启电脑。"
	}
	checks = append(checks, accel)

	img := envCheck{
		name:     "系统镜像",
		ok:       len(env.SystemImages) > 0,
		blocking: true,
		detail:   fmt.Sprintf("已安装 %d 个", len(env.SystemImages)),
	}
	if !img.ok {
		img.impact = "没有可用镜像，无法新建实例。"
		img.fix = "切换到上方「系统镜像」标签页，选择镜像点击「下载安装」或「导入 .zip」。"
	}
	checks = append(checks, img)
	return checks
}

// envIssues counts the failed checks that block core features and the ones that only limit optional features.
func envIssues(checks []envCheck) (blocking, optional int) {
	for _, c := range checks {
		switch {
		case c.ok:
		case c.blocking:
			blocking++
		default:
			optional++
		}
	}
	return blocking, optional
}

// imageRow is one system image in the guide: recommended ones come first, then any other installed ones.
type imageRow struct {
	pkg         string
	recommended bool
	installed   bool
}

func imageRows(env service.EnvStatus) []imageRow {
	installed := make(map[string]bool, len(env.SystemImages))
	for _, p := range env.SystemImages {
		installed[p] = true
	}
	var rows []imageRow
	seen := map[string]bool{}
	for _, p := range defaultImageSuggestions {
		rows = append(rows, imageRow{pkg: p, recommended: true, installed: installed[p]})
		seen[p] = true
	}
	for _, p := range env.SystemImages {
		if !seen[p] {
			rows = append(rows, imageRow{pkg: p, installed: true})
			seen[p] = true
		}
	}
	return rows
}

// imageChoices lists images for the create form: installed ones first, then recommended ones still missing,
// plus any custom catalog entries.
func imageChoices(env service.EnvStatus, cat ...service.ImageItem) []string {
	out := append([]string(nil), env.SystemImages...)
	have := map[string]bool{}
	for _, p := range out {
		have[p] = true
	}
	for _, p := range defaultImageSuggestions {
		if !have[p] {
			out = append(out, p)
			have[p] = true
		}
	}
	for _, it := range cat {
		if it.ID != "" && !have[it.ID] {
			out = append(out, it.ID)
			have[it.ID] = true
		}
	}
	return out
}

func imageInstalled(env service.EnvStatus, pkg string) bool {
	for _, p := range env.SystemImages {
		if p == pkg {
			return true
		}
	}
	return false
}

// envHealthText is the one-line status shown in the navigation.
func envHealthText(loaded bool, checks []envCheck) string {
	if !loaded {
		return "正在检查运行环境…"
	}
	blocking, optional := envIssues(checks)
	switch {
	case blocking > 0:
		return fmt.Sprintf("环境有 %d 项需处理", blocking+optional)
	case optional > 0:
		return fmt.Sprintf("环境可用，%d 项可选组件缺失", optional)
	}
	return "运行环境已就绪"
}

const installedImagePrefix = "✓ 已安装  "

func imageChoiceLabel(env service.EnvStatus, pkg string) string {
	if imageInstalled(env, pkg) {
		return installedImagePrefix + pkg
	}
	return pkg
}

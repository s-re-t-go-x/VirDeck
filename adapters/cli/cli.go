package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"
	"virdeck/config"

	"virdeck/assets"
	"virdeck/core/emu"
	"virdeck/core/screenlayout"
	"virdeck/core/service"
)

var cliStdin io.Reader = os.Stdin

func resolveCLIProxy(fs *flag.FlagSet, flagName, flagVal string, fromStdin bool) (string, error) {
	flagSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == flagName {
			flagSet = true
		}
	})
	if fromStdin && flagSet {
		return "", errors.New("不能同时通过命令行参数与 -proxy-stdin 指定代理地址")
	}
	if fromStdin {
		raw, err := io.ReadAll(io.LimitReader(cliStdin, 4097))
		if err != nil {
			return "", fmt.Errorf("从标准输入读取代理地址失败: %w", err)
		}
		if len(raw) > 4096 {
			return "", errors.New("标准输入代理地址超过长度限制")
		}
		trimmed := strings.TrimRight(string(raw), "\r\n")
		trimmed = strings.TrimSpace(trimmed)
		if strings.ContainsAny(trimmed, "\r\n\x00") {
			return "", errors.New("代理地址不能包含换行符或空字符")
		}
		return trimmed, nil
	}
	if flagSet {
		return flagVal, nil
	}
	if v, ok := os.LookupEnv("VIRDECK_PROXY"); ok {
		return strings.TrimSpace(v), nil
	}
	return "", nil
}

// Run executes the CLI adapter against the provided core Service.
func Run(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	if len(args) == 0 {
		printUsage(out)
		return nil
	}

	sub := strings.ToLower(strings.TrimSpace(args[0]))
	rest := args[1:]

	switch sub {
	case "help", "-h", "--help":
		printUsage(out)
		return nil
	case "env", "check":
		return runEnv(ctx, svc, out)
	case "images", "catalog":
		return runImages(ctx, svc, rest, out)
	case "bootstrap":
		return runBootstrap(ctx, svc, rest, out)
	case "list", "ls":
		return runList(ctx, svc, out)
	case "create":
		return runCreate(ctx, svc, rest, out)
	case "start":
		return runStart(ctx, svc, rest, out)
	case "stop":
		return runStop(ctx, svc, rest, out)
	case "delete", "rm":
		return runDelete(ctx, svc, rest, out)
	case "proxy":
		return runProxy(ctx, svc, rest, out)
	case "install":
		return runInstall(ctx, svc, rest, out)
	case "shell":
		return runShell(ctx, svc, rest, out)
	case "screen", "scrcpy":
		return runScreen(ctx, svc, rest, out)
	case "geo", "gps":
		return runGeo(ctx, svc, rest, out)
	case "screenshot", "screencap":
		return runScreenshot(ctx, svc, rest, out)
	case "apps":
		return runApps(ctx, svc, rest, out)
	case "disk-clean":
		return runDiskClean(ctx, svc, rest, out)
	case "backup":
		return runBackup(ctx, svc, rest, out)
	case "restore":
		return runRestore(ctx, svc, rest, out)
	case "__socks-bridge":
		return runSocksBridgeHelper(rest, out)
	default:
		printUsage(out)
		return fmt.Errorf("未知子命令: %s", sub)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, "%s %s 命令行工具\n\n", assets.Product.Name, assets.Product.Version)
	fmt.Fprintln(w, `用法:
  VirDeck cli <command> [arguments]
  virdeck-cli <command> [arguments]

可用命令:
  env                                    自检 VirDeckData、宿主硬件加速、adb/emulator/scrcpy 路径与可用镜像
  images [-sync] [-download <包ID>] [-import <包ID> -zip <路径>] [-rm <包ID>]
                                         管理系统镜像目录（推荐 + 官方 XML 同步 + 本地导入 + 删除）
  bootstrap [-tool all|adb|emulator|scrcpy] [-zip <离线包路径>]
                                         一键下载补齐或离线导入基础运行工具
  list | ls                              列出所有已注册的模拟器实例及状态
  create -name <前缀> -image <镜像包> [-count <数量>] [-mem <MB>] [-cores <核数>] [-storage <GB>] [-res 720p|1080p|tablet] [-quickboot=false] [-proxy <url>|-proxy-stdin] [-headless=true]
                                         纯 Go 批量创建干净的 AVD 实例（无 JDK 依赖；也可通过 VIRDECK_PROXY 环境变量传代理）
  start [-wait=true] <ID|名称|all>...    启动一个或多个实例（默认等待引导完成）
  stop <ID|名称|all>...                  优雅停止一个或多个实例（超时强杀进程树）
  delete | rm <ID|名称>...               停止并彻底删除指定实例
  proxy -id <ID或名称> [-url <代理地址>|-proxy-stdin]                    设置或清空实例的网络代理（传 -url "" 清空，支持 VIRDECK_PROXY）
  install -apk <路径> [-start] [-launch] [-grant] [-screen] <ID|名称|running|all>...
                                         向指定实例安装 APK（可选自动开机/拉起应用/授常规权限/打开投屏）
  shell -cmd "<命令>" <ID|名称|running>...
                                         向指定（或全部运行中）实例并发执行 adb shell 命令
  screen [-x X -y Y -width W -height H] <ID或名称>                      拉起指定运行中实例的 scrcpy 独立投屏窗口
  geo -lat <纬度> -lon <经度> [<ID|名称|running>...]                    向运行中实例注入虚拟 GPS 坐标
  screenshot [-out <目录>] [<ID|名称|running>...]                       通过 adb exec-out 批量截取实例屏幕为 PNG
  apps -id <ID或名称> [-all] [-launch|-stop|-clear|-uninstall <包名>]   查看或管理运行中实例内已安装应用
  disk-clean -id <ID或名称>                                              运行中执行 fstrim，停机状态下清理缓存并压缩 qcow2 镜像
  backup -id <ID或名称> [-out <zip路径>] [-include-proxy]                 导出已停止实例为便携式 .zip 备份（默认不含代理）
  restore -zip <zip路径> [-name <实例名称>] [-include-proxy]            从备份压缩包还原为新实例（默认不继承代理）`)
}

func runEnv(ctx context.Context, svc service.Service, out io.Writer) error {
	st, err := svc.CheckEnv(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "数据根目录:     %s\n", emptyFallback(st.DataDir, "(未检测到)"))
	if st.EffectiveDataDir != "" && st.EffectiveDataDir != st.DataDir {
		mode := st.PathCompatMode
		switch mode {
		case "shortpath":
			mode = "8.3 短路径"
		case "junction":
			mode = "NTFS 目录联接"
		}
		fmt.Fprintf(out, "运行映射路径:   %s (%s)\n", st.EffectiveDataDir, mode)
	}
	fmt.Fprintf(out, "SDK 根目录:     %s\n", emptyFallback(st.SDKRoot, "(未检测到)"))
	fmt.Fprintf(out, "AVD 存储目录:   %s\n", emptyFallback(st.AVDHome, "(未检测到)"))
	fmt.Fprintf(out, "emulator 路径:  %s\n", emptyFallback(st.EmulatorPath, "(未找到)"))
	fmt.Fprintf(out, "adb 路径:       %s\n", emptyFallback(st.ADBPath, "(未找到)"))
	fmt.Fprintf(out, "scrcpy 路径:    %s\n", emptyFallback(st.ScrcpyPath, "(未找到)"))
	fmt.Fprintf(out, "硬件加速可用:   %v (%s)\n", st.AccelOK, st.AccelMessage)
	fmt.Fprintf(out, "路径检查:       ASCII=%v, 可写=%v\n", st.PathASCIIOK, st.PathWritableOK)
	if st.PathWarning != "" {
		fmt.Fprintf(out, "路径提醒:       %s\n", st.PathWarning)
	}
	fmt.Fprintf(out, "本地系统镜像数: %d\n", len(st.SystemImages))
	for _, img := range st.SystemImages {
		fmt.Fprintf(out, "  - %s\n", img)
	}
	return nil
}

func runImages(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("images", flag.ContinueOnError)
	fs.SetOutput(out)
	syncFlag := fs.Bool("sync", false, "从 Google 官方仓库同步当前宿主架构镜像列表")
	dlID := fs.String("download", "", "下载并安装指定镜像包 ID")
	impID := fs.String("import", "", "要导入的目标镜像包 ID")
	zipPath := fs.String("zip", "", "导入时使用的本地 .zip 文件路径")
	rmID := fs.String("rm", "", "删除指定的本地已安装镜像包 ID")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *syncFlag {
		n, err := svc.SyncCatalog(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "已从官方仓库同步 %d 个镜像条目\n", n)
	}
	if strings.TrimSpace(*dlID) != "" {
		return svc.DownloadSystemImage(ctx, *dlID)
	}
	if strings.TrimSpace(*impID) != "" {
		return svc.ImportSystemImageZip(ctx, *impID, *zipPath)
	}
	if strings.TrimSpace(*rmID) != "" {
		return svc.DeleteSystemImage(ctx, *rmID)
	}

	items, err := svc.ListCatalog(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tSOURCE\tID\tNAME")
	for _, it := range items {
		st := "未安装"
		if it.Installed {
			st = "已安装"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", st, it.Source, it.ID, it.Name)
	}
	return tw.Flush()
}

func runBootstrap(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	fs.SetOutput(out)
	tool := fs.String("tool", "all", "工具名称: all | adb | emulator | scrcpy")
	zipPath := fs.String("zip", "", "可选本地 .zip 压缩包路径（离线导入）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*zipPath) != "" {
		if err := svc.ImportBaseToolZip(ctx, *tool, *zipPath); err != nil {
			return err
		}
		fmt.Fprintf(out, "已成功从本地压缩包导入 %s\n", *tool)
		return nil
	}
	if err := svc.EnsureBaseTool(ctx, *tool); err != nil {
		return err
	}
	fmt.Fprintf(out, "基础组件 %s 已补齐完成\n", *tool)
	return nil
}

func runList(ctx context.Context, svc service.Service, out io.Writer) error {
	items, err := svc.List(ctx)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(out, "暂无实例。使用 `create` 命令创建新实例。")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATE\tPORT\tMEM(MB)\tCORES\tHEADLESS\tPROXY\tSYSTEM IMAGE")
	for _, it := range items {
		portStr := "-"
		if it.Port > 0 {
			portStr = fmt.Sprintf("%d", it.Port)
		}
		proxyStr := emptyFallback(config.RedactProxyURL(it.Proxy), "-")
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%v\t%s\t%s\n",
			it.ID, it.Name, it.State, portStr, it.MemoryMB, it.Cores, it.Headless, proxyStr, it.SystemImage)
	}
	return tw.Flush()
}

func runCreate(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.SetOutput(out)
	name := fs.String("name", config.Values.Create.Name, "实例名称前缀")
	image := fs.String("image", "", "系统镜像包路径 (例如 system-images;android-34;google_apis_playstore;x86_64)")
	count := fs.Int("count", config.Values.Create.Count, "创建数量")
	mem := fs.Int("mem", config.Values.Create.MemoryMB, "内存大小 (MB)")
	cores := fs.Int("cores", config.Values.Create.Cores, "CPU 核数")
	res := fs.String("res", config.Values.Create.Resolution, "分辨率预设 (720p、1080p、tablet 或 宽x高@DPI)")
	storage := fs.Int("storage", 6, "实例存储容量 (GB，4–128)")
	quickboot := fs.Bool("quickboot", false, "启用快速启动快照")
	proxy := fs.String("proxy", "", "代理地址 (支持 http:// 或 socks5://；也可用 VIRDECK_PROXY)")
	proxyStdin := fs.Bool("proxy-stdin", false, "从标准输入读取代理地址（避免在命令行历史与进程列表暴露凭据）")
	headless := fs.Bool("headless", config.Values.Create.Headless, "无窗口省资源运行 (-no-window)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolvedProxy, err := resolveCLIProxy(fs, "proxy", *proxy, *proxyStdin)
	if err != nil {
		return err
	}

	sysImg := strings.TrimSpace(*image)
	if sysImg == "" {
		if imgs, _ := svc.ListSystemImages(ctx); len(imgs) > 0 {
			sysImg = imgs[0]
			fmt.Fprintf(out, "未指定 -image，自动选用本地镜像: %s\n", sysImg)
		}
	}

	created, err := svc.Create(ctx, service.CreateSpec{
		NamePrefix:  *name,
		Count:       *count,
		SystemImage: sysImg,
		MemoryMB:    *mem,
		Cores:       *cores,
		StorageGB:   *storage,
		Resolution:  *res,
		QuickBoot:   *quickboot,
		Proxy:       resolvedProxy,
		Headless:    *headless,
	})
	for _, inst := range created {
		fmt.Fprintf(out, "已创建实例: %s (%s)\n", inst.Name, inst.ID)
	}
	return err
}

func runStart(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(out)
	wait := fs.Bool("wait", true, "等待实例完成 Android 引导 (sys.boot_completed=1)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	targets := fs.Args()
	if len(targets) == 0 {
		return errors.New("请指定要启动的实例 ID、名称或 all")
	}
	ids, err := resolveTargets(ctx, svc, targets)
	if err != nil {
		return err
	}

	evCh, cancelSub := svc.Subscribe()
	defer cancelSub()

	if err := svc.Start(ctx, ids...); err != nil {
		return err
	}
	if !*wait {
		fmt.Fprintf(out, "已触发启动 %d 个实例\n", len(ids))
		return nil
	}

	fmt.Fprintf(out, "正在等待 %d 个实例完成引导 (按 Ctrl+C 取消等待)...\n", len(ids))
	for {
		allDone := true
		items, _ := svc.List(ctx)
		for _, id := range ids {
			for _, it := range items {
				if it.ID == id && it.State == service.Starting {
					allDone = false
				}
			}
		}
		if allDone {
			return runList(ctx, svc, out)
		}
		select {
		case ev := <-evCh:
			if ev.Message != "" {
				fmt.Fprintf(out, "[%s] %s\n", ev.Timestamp, config.RedactText(ev.Message))
			}
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func runStop(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("请指定要停止的实例 ID、名称、running 或 all")
	}
	ids, err := resolveTargets(ctx, svc, args)
	if err != nil {
		return err
	}
	if err := svc.Stop(ctx, ids...); err != nil {
		return err
	}
	fmt.Fprintf(out, "已停止 %d 个实例\n", len(ids))
	return nil
}

func runDelete(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("请指定要删除的实例 ID 或名称")
	}
	ids, err := resolveTargets(ctx, svc, args)
	if err != nil {
		return err
	}
	if err := svc.Delete(ctx, ids...); err != nil {
		return err
	}
	fmt.Fprintf(out, "已删除 %d 个实例\n", len(ids))
	return nil
}

func runProxy(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	fs.SetOutput(out)
	target := fs.String("id", "", "实例 ID 或名称")
	url := fs.String("url", "", "网络代理地址 (传空字符串清空，也可用 VIRDECK_PROXY)")
	proxyStdin := fs.Bool("proxy-stdin", false, "从标准输入读取代理地址（避免在命令行历史与进程列表暴露凭据）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*target) == "" {
		return errors.New("请通过 -id 指定实例 ID 或名称")
	}
	resolvedProxy, err := resolveCLIProxy(fs, "url", *url, *proxyStdin)
	if err != nil {
		return err
	}
	ids, err := resolveTargets(ctx, svc, []string{*target})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := svc.SetProxy(ctx, id, resolvedProxy); err != nil {
			return err
		}
		fmt.Fprintf(out, "已更新实例 %s 的代理为 %q\n", id, config.RedactProxyURL(resolvedProxy))
	}
	return nil
}

func runInstall(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(out)
	apk := fs.String("apk", "", "要安装的 APK / APKS / XAPK 文件路径")
	autoStart := fs.Bool("start", false, "若目标实例未启动则自动开机并等待就绪")
	launch := fs.Bool("launch", false, "安装完成后自动解析并拉起桌面入口")
	grant := fs.Bool("grant", false, "安装时自动授予常规运行时权限 (-g)")
	screen := fs.Bool("screen", false, "完成后自动打开 scrcpy 投屏（仅无头实例）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*apk) == "" {
		return errors.New("请通过 -apk 指定 APK 文件路径")
	}
	targets := fs.Args()
	if len(targets) == 0 {
		targets = []string{"running"}
	}
	ids, err := resolveTargets(ctx, svc, targets)
	if err != nil {
		return err
	}
	if *autoStart || *launch || *grant || *screen {
		err = svc.InstallApp(ctx, service.InstallAppRequest{
			InstanceIDs:      ids,
			APKPath:          *apk,
			GrantPermissions: *grant,
			LaunchApp:        *launch,
			OpenScreen:       *screen,
		})
	} else {
		err = svc.InstallAPK(ctx, ids, *apk)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "已成功向 %d 个实例安装 %s\n", len(ids), *apk)
	return nil
}

func runShell(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("shell", flag.ContinueOnError)
	fs.SetOutput(out)
	cmdStr := fs.String("cmd", "", "要执行的 adb shell 命令")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*cmdStr) == "" {
		return errors.New("请通过 -cmd 指定要执行的命令")
	}
	targets := fs.Args()
	if len(targets) == 0 {
		targets = []string{"running"}
	}
	ids, err := resolveTargets(ctx, svc, targets)
	if err != nil {
		return err
	}
	results, err := svc.Shell(ctx, ids, *cmdStr)
	for id, res := range results {
		fmt.Fprintf(out, "=== [%s] ===\n%s\n", id, res)
	}
	return err
}

func runScreen(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("screen", flag.ContinueOnError)
	fs.SetOutput(out)
	x := fs.Int("x", 0, "投屏窗口初始 X 坐标")
	y := fs.Int("y", 0, "投屏窗口初始 Y 坐标")
	width := fs.Int("width", 0, "投屏客户区宽度")
	height := fs.Int("height", 0, "投屏客户区高度")
	if err := fs.Parse(args); err != nil {
		return err
	}
	positioned := false
	fs.Visit(func(*flag.Flag) { positioned = true })
	if positioned && (*width <= 0 || *height <= 0 || *width > 32768 || *height > 32768 || *x < -32768 || *x > 32768 || *y < -32768 || *y > 32768) {
		return errors.New("指定坐标时，宽高需为 1–32768，坐标范围为 -32768–32768")
	}
	ids, err := resolveTargets(ctx, svc, fs.Args())
	if err != nil {
		return err
	}
	for _, id := range ids {
		if positioned {
			opener, ok := svc.(interface {
				OpenScreenAt(context.Context, string, screenlayout.Rect) error
			})
			if !ok {
				return errors.New("当前后端不支持指定投屏窗口坐标")
			}
			err = opener.OpenScreenAt(ctx, id, screenlayout.Rect{X: *x, Y: *y, Width: *width, Height: *height})
		} else {
			err = svc.OpenScreen(ctx, id)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "已拉起实例 %s 的 scrcpy 窗口\n", id)
	}
	return nil
}

func runGeo(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("geo", flag.ContinueOnError)
	fs.SetOutput(out)
	lat := fs.Float64("lat", 0, "目标纬度 (-90 ~ 90)")
	lon := fs.Float64("lon", 0, "目标经度 (-180 ~ 180)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	hasLat, hasLon := false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "lat":
			hasLat = true
		case "lon":
			hasLon = true
		}
	})
	if !hasLat || !hasLon {
		return errors.New("请同时通过 -lat 与 -lon 指定经纬度")
	}
	targets := fs.Args()
	if len(targets) == 0 {
		targets = []string{"running"}
	}
	ids, err := resolveTargets(ctx, svc, targets)
	if err != nil {
		return err
	}
	if err := svc.SetLocation(ctx, ids, *lat, *lon); err != nil {
		return err
	}
	fmt.Fprintf(out, "已对 %d 个实例注入虚拟定位 (lat=%.6f, lon=%.6f)\n", len(ids), *lat, *lon)
	return nil
}

func runScreenshot(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("screenshot", flag.ContinueOnError)
	fs.SetOutput(out)
	outDir := fs.String("out", "", "截图输出目录（默认为 VirDeckData/screenshots）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	targets := fs.Args()
	if len(targets) == 0 {
		targets = []string{"running"}
	}
	ids, err := resolveTargets(ctx, svc, targets)
	if err != nil {
		return err
	}
	paths, err := svc.CaptureScreenshots(ctx, ids, *outDir)
	for id, p := range paths {
		fmt.Fprintf(out, "[%s] %s\n", id, p)
	}
	return err
}

func runApps(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("apps", flag.ContinueOnError)
	fs.SetOutput(out)
	target := fs.String("id", "", "目标实例 ID 或名称")
	allApps := fs.Bool("all", false, "包含系统应用（默认仅列出第三方应用）")
	launchPkg := fs.String("launch", "", "启动指定包名")
	stopPkg := fs.String("stop", "", "强行停止指定包名")
	clearPkg := fs.String("clear", "", "清除指定包名数据")
	uninstallPkg := fs.String("uninstall", "", "卸载指定包名")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*target) == "" {
		return errors.New("请通过 -id 指定实例 ID 或名称")
	}
	ids, err := resolveTargets(ctx, svc, []string{*target})
	if err != nil {
		return err
	}
	id := ids[0]
	var op, pkg string
	for _, item := range []struct {
		op, val string
	}{
		{"launch", *launchPkg},
		{"stop", *stopPkg},
		{"clear", *clearPkg},
		{"uninstall", *uninstallPkg},
	} {
		if strings.TrimSpace(item.val) != "" {
			if op != "" {
				return errors.New("一次只能指定一个操作（-launch / -stop / -clear / -uninstall）")
			}
			op, pkg = item.op, strings.TrimSpace(item.val)
		}
	}
	if op != "" {
		if err := svc.ManageInstalledApp(ctx, id, pkg, op); err != nil {
			return err
		}
		fmt.Fprintf(out, "已对实例 %s 的 %s 执行 %s\n", id, pkg, op)
		return nil
	}
	apps, err := svc.ListInstalledApps(ctx, id, !*allApps)
	if err != nil {
		return err
	}
	for _, app := range apps {
		fmt.Fprintln(out, app.PackageName)
	}
	return nil
}

func runDiskClean(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("disk-clean", flag.ContinueOnError)
	fs.SetOutput(out)
	target := fs.String("id", "", "目标实例 ID 或名称")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*target) == "" {
		return errors.New("请通过 -id 指定实例 ID 或名称")
	}
	ids, err := resolveTargets(ctx, svc, []string{*target})
	if err != nil {
		return err
	}
	res, err := svc.CleanInstanceDisk(ctx, ids[0])
	if err != nil {
		return err
	}
	fmt.Fprintln(out, res.Message)
	return nil
}

func runBackup(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(out)
	target := fs.String("id", "", "要备份的已停止实例 ID 或名称")
	destZip := fs.String("out", "", "输出 .zip 路径（默认为 VirDeckData/backups/）")
	includeProxy := fs.Bool("include-proxy", false, "在备份中包含实例代理配置（可能含敏感凭据）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*target) == "" {
		return errors.New("请通过 -id 指定实例 ID 或名称")
	}
	ids, err := resolveTargets(ctx, svc, []string{*target})
	if err != nil {
		return err
	}
	res, err := svc.BackupInstanceWithOptions(ctx, ids[0], *destZip, service.BackupOptions{IncludeProxy: *includeProxy})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "已备份至 %s (%d 字节)\n", res.Path, res.SizeBytes)
	return nil
}

func runRestore(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(out)
	srcZip := fs.String("zip", "", "要还原的 .zip 备份文件路径")
	name := fs.String("name", "", "可选的新实例显示名称")
	includeProxy := fs.Bool("include-proxy", false, "从备份元数据中继承网络代理配置（仅限本人可信备份）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*srcZip) == "" {
		return errors.New("请通过 -zip 指定备份文件路径")
	}
	inst, err := svc.RestoreInstanceWithOptions(ctx, *srcZip, *name, service.RestoreOptions{IncludeProxy: *includeProxy})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "已还原实例 %s (%s)\n", inst.Name, inst.ID)
	return nil
}

func runSocksBridgeHelper(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("__socks-bridge", flag.ContinueOnError)
	fs.SetOutput(out)
	watchPort := fs.Int("watch-port", 0, "监视的模拟器控制台端口")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return emu.RunSOCKSBridgeHelper(*watchPort, os.Stdin, out)
}

func resolveTargets(ctx context.Context, svc service.Service, raw []string) ([]string, error) {
	items, err := svc.List(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	seen := make(map[string]bool)
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	for _, token := range raw {
		t := strings.TrimSpace(token)
		switch strings.ToLower(t) {
		case "all":
			for _, it := range items {
				add(it.ID)
			}
		case "running":
			for _, it := range items {
				if it.State == service.Running {
					add(it.ID)
				}
			}
		default:
			matched := false
			for _, it := range items {
				if it.ID == t || strings.EqualFold(it.Name, t) {
					add(it.ID)
					matched = true
				}
			}
			if !matched {
				return nil, fmt.Errorf("未找到匹配的实例: %s", t)
			}
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("没有符合条件的实例")
	}
	return ids, nil
}

func emptyFallback(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

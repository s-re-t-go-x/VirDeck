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
	"virdeck/core/screenlayout"
	"virdeck/core/service"
)

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
	case "clone":
		return runClone(ctx, svc, rest, out)
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
  create -name <前缀> -image <镜像包> [-count <数量>] [-mem <MB>] [-cores <核数>] [-res 720p|1080p] [-proxy <url>] [-headless=true]
                                         纯 Go 批量创建干净的 AVD 实例（无 JDK 依赖）
  clone -src <ID或名称> [-count 1]       从已停止的源实例克隆新实例（可选功能）
  start [-wait=true] <ID|名称|all>...    启动一个或多个实例（默认等待引导完成）
  stop <ID|名称|all>...                  优雅停止一个或多个实例（超时强杀进程树）
  delete | rm <ID|名称>...               停止并彻底删除指定实例
  proxy -id <ID或名称> -url <代理地址>   设置或清空实例的 HTTP 代理（传空字符串清空）
  install -apk <路径> <ID|名称|running>...
                                         向指定（或全部运行中）实例并发安装 APK
  shell -cmd "<命令>" <ID|名称|running>...
                                         向指定（或全部运行中）实例并发执行 adb shell 命令
  screen [-x X -y Y -width W -height H] <ID或名称>                      拉起指定运行中实例的 scrcpy 独立投屏窗口`)
}

func runEnv(ctx context.Context, svc service.Service, out io.Writer) error {
	st, err := svc.CheckEnv(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "数据根目录:     %s\n", emptyFallback(st.DataDir, "(未检测到)"))
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
		proxyStr := emptyFallback(it.Proxy, "-")
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
	res := fs.String("res", config.Values.Create.Resolution, "分辨率预设 (720p 或 1080p)")
	proxy := fs.String("proxy", "", "HTTP 代理地址 (例如 http://127.0.0.1:7890)")
	headless := fs.Bool("headless", config.Values.Create.Headless, "无窗口省资源运行 (-no-window)")
	if err := fs.Parse(args); err != nil {
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
		Resolution:  *res,
		Proxy:       *proxy,
		Headless:    *headless,
	})
	for _, inst := range created {
		fmt.Fprintf(out, "已创建实例: %s (%s)\n", inst.Name, inst.ID)
	}
	return err
}

func runClone(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("clone", flag.ContinueOnError)
	fs.SetOutput(out)
	src := fs.String("src", "", "源实例 ID 或名称")
	count := fs.Int("count", config.Values.CloneCount, "克隆数量")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*src) == "" {
		return errors.New("请通过 -src 指定源实例 ID 或名称")
	}
	ids, err := resolveTargets(ctx, svc, []string{*src})
	if err != nil {
		return err
	}
	cloned, err := svc.Clone(ctx, ids[0], *count)
	for _, inst := range cloned {
		fmt.Fprintf(out, "已克隆实例: %s (%s)\n", inst.Name, inst.ID)
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
				fmt.Fprintf(out, "[%s] %s\n", ev.Timestamp, ev.Message)
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
	url := fs.String("url", "", "HTTP 代理地址 (为空则清除代理)")
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
	for _, id := range ids {
		if err := svc.SetProxy(ctx, id, *url); err != nil {
			return err
		}
		fmt.Fprintf(out, "已更新实例 %s 的代理为 %q\n", id, *url)
	}
	return nil
}

func runInstall(ctx context.Context, svc service.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(out)
	apk := fs.String("apk", "", "要安装的 APK 文件路径")
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
	if err := svc.InstallAPK(ctx, ids, *apk); err != nil {
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

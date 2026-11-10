# 当前开发待办 (TODO)

更新日期：2026-11-09

本文档记录当前已完成的基础功能、基于实际体验梳理出的**「交互与人性化改进」待办清单**，以及后续的「工作会话」与「跨平台（macOS / Linux）适配」扩展计划。架构细节与历史归档见 [开发与架构文档](development.md)。

---

## 一、已完成阶段：基础功能的人性化与交互优化（2026-11-10 已全部实现）

针对刚交付的四项基础功能在日常高频操作中暴露出的路径繁琐、反馈缺失和盲填路径问题，计划按以下四个模块做针对性打磨：

### 1. 文件收发体验闭环（最高优先级，2026-11-09 已实现）
关联代码：[`adapters/mygonative/transfer.go`](../adapters/mygonative/transfer.go)、[`core/service/transfer.go`](../core/service/transfer.go)、[`core/adb/transfer.go`](../core/adb/transfer.go)

- [x] **接通远程目录文件列表（告别盲填路径）**：在「接收到 Windows」模式下，利用底层已实现的 [`Manager.ListTransferFiles`](../core/service/transfer.go) 与 [`Client.TransferList`](../core/adb/transfer.go)，在选择常用目录（如 `Download`、`Pictures`、`DCIM`）或输入目录后，直接在弹窗内列出该目录下的文件与子文件夹，支持点击选中具体目标，避免用户手动查文件名或误拉取整个大目录。
- [x] **优化发送到安卓的目标目录结构与相册刷新**：
  - 将当前 `mktemp -d transfer-XXXXXXXX` 生成的无规律随机子目录，改为直接放入目标目录（依赖已有同名自动编号 `name (2).ext` 防覆盖）或使用带可读时间戳的子目录名（如 `20261109-1430`）。
  - 发送完成后自动触发 Android 媒体扫描（`am broadcast -a android.intent.action.MEDIA_SCANNER_SCAN_FILE`），使发送的图片/视频在安卓相册及社交软件选图界面立即可见。
- [x] **完善本地文件预览、路径记忆与一键打开目录**：
  - 「发送到 Android」选择文件后，在界面展示已选文件名摘要（而不仅显示「已选择 N 个文件」），支持清空或重选。
  - 「接收到 Windows」默认将本地接收目录初始化为系统 `Downloads`（或记住上次使用的接收目录），避免每次打开弹窗都需重新选择。
  - 传输完成后，在弹窗状态区明确展示实际发送目标路径或本地导出文件夹路径，并提供 **「打开接收文件夹」** 按钮（调用 `mygo.Shell.OpenPath`）。

实现说明：发送目录使用 `YYYYMMDD-HHMMSS-唯一后缀`，保留原有隔离与同名编号保护；相册刷新失败单独记入日志。接收目录在本次运行期间记忆，首次默认使用用户目录下的 `Downloads`；输入路径后点击「读取目录」，切换常用目录或来源实例时自动刷新。自动化验证已覆盖传输与目录选择，真实 Android 相册可见性仍需实机验收。

### 2. 投屏与声音的快捷控制（2026-11-09 已实现）
关联代码：[`adapters/mygonative/projection_settings.go`](../adapters/mygonative/projection_settings.go)、[`adapters/mygonative/appearance.go`](../adapters/mygonative/appearance.go)、[`adapters/mygonative/app.go`](../adapters/mygonative/app.go)

- [x] **增加场景化一键预设档位**：在全局「设置 → 投屏与声音」及单实例「投屏设置」表单顶部，提供三档快捷预设按钮，点击即可自动填充参数（仍保留下方数值微调能力）：
  - **省资源多开**：`15 FPS · 最长边 960 px · 2 Mbps`（适合后台多开挂机巡检）
  - **均衡日常（默认）**：`60 FPS · 最长边 1280 px · 8 Mbps`
  - **高清流畅**：`60 FPS · 最长边 1920 px · 16 Mbps`
- [x] **详情栏展示投屏配置状态与「一键开关声音」快捷入口**：
  - 在右侧「实例详情」中显示当前实例的投屏配置摘要（如 `跟随全局 (60 FPS · 静音)` 或 `独立配置 (15 FPS · 开启声音)`），一眼区分哪些实例覆盖了全局设置。
  - 提供单实例快捷切换声音并自动重开投屏的操作入口，解决临时听取某台实例语音/视频时需多层弹窗勾选的问题。
- [x] **支持批量投屏设置与全局保存后一键重开**：
  - 多选实例时开放「投屏设置」，支持将多台实例批量设为同一预设或批量恢复「跟随全局设置」。
  - 在全局「设置 → 投屏与声音」中增加 **「保存并重开已打开投屏」** 按钮，修改全局参数后可一键刷新当前所有跟随全局的投屏窗口。

实现说明：预设来自编译期 `config/defaults.json`，只改变画面参数并保留声音选择；多选配置不一致时需明确选择统一预设或继承方式。详情栏顶部操作区提供紧凑的「静音 / 有声」快捷切换按钮，参数摘要并入「设备配置」的「投屏配置」行；当快捷切换后的参数与全局设置一致时自动恢复跟随全局，并仅重开已打开投屏；全局保存重开跳过独立配置与忙碌实例，失败可在任务面板查看。宽窄窗口已渲染核对；实际声音和窗口位置沿用现有重开链路，仍建议实机回归。

### 3. 分组归属可视化与管理交互优化（2026-11-09 已实现）
关联代码：[`adapters/mygonative/groups.go`](../adapters/mygonative/groups.go)、[`adapters/mygonative/app.go`](../adapters/mygonative/app.go)

- [x] **在实例列表与详情栏显示所属分组**：
  - 当处于「全部分组」视图时，在实例列表行中展示该实例所属的分组标识（未分组可不显示或弱化显示），避免用户看不出各实例归属。
  - 在右侧「实例详情」中展示当前所属分组，并支持直接在详情栏下拉切换分组。
- [x] **分组筛选器显示实例计数**：顶部分组下拉框（`全部分组`、`未分组` 及各自定义分组）旁附带显示对应的实例数量（如 `工作 (3)`、`未分组 (1)`）。
- [x] **重构「管理分组」弹窗与新建归组流程**：
  - 将「管理分组」弹窗中混用同一个下拉框切换「新建分组 / 已有分组」的交互，优化为直观的「已有分组列表（显示成员数、重命名、删除）+ 独立新建分组输入区」，并在创建、改名、删除成功后给出明确的视觉反馈提示。
  - 在「新建实例」与「克隆实例」弹窗中增加可选的「所属分组」字段（分别默认继承当前正在筛选的分组与源实例分组）；在顶部「移动到分组」下拉菜单及详情栏头部状态旁的分组胶囊中支持直接切换分组或点击「+ 新建分组并移入」。

实现说明：分组计数按完整实例列表统计，不随状态和搜索缩减；列表行将分组名与系统镜像同行展示以保持紧凑两行布局，详情栏头部状态行旁以轻量胶囊菜单承载分组切换；管理弹窗将成员数、重命名、删除与新建区分开。新建并移入固定原选中目标，移入失败可重试而不重复创建分组；实例创建或克隆后的归组失败会保留实例并明确提示。

### 4. 错误反馈触达与日志可复制性（2026-11-10 已实现）
关联代码：[`adapters/mygonative/app.go`](../adapters/mygonative/app.go)、[`adapters/mygonative/tasks.go`](../adapters/mygonative/tasks.go)

- [x] **窄窗口与列表行的异常原因直达**：
  - 为实例列表行的 `✖ 异常` 状态绑定 `Tooltip(inst.LastError)` 悬浮提示，并在状态下方展示单行简短错误摘要（同样带完整错误 Tooltip），解决收起详情栏或窄窗口时看不到报错原因的问题；详情栏「最近错误」上移至主操作区正下方，无需滚动即可首屏看到。
  - 移除底部任务栏「查看失败原因」按钮在窄窗口（`< 900px`）下的隐藏限制，确保任何窗口尺寸下任务失败都能一键展开定位。
- [x] **启动中阶段耗时反馈**：实例处于 `◐ 启动中` 时，动态显示已等待秒数（`◐ 启动中 · 18s`）及当前引导阶段提示（列表行「等待桌面就绪」、详情栏「正在引导 Android 系统，等待桌面就绪…」），避免冷启动耗时较长时用户误以为程序卡死。
- [x] **支持一键复制日志与错误信息**：在底部「运行日志」面板（「清空日志」旁）增加 **「复制日志」** 按钮，并在实例详情的「最近错误」区域增加 **「复制错误」** 按钮，一键写入系统剪贴板方便排查。

---

## 二、已完成阶段：四项基础功能（2026-11-09 已交付）

### 1. 省资源与投屏参数
- [x] 在设置界面集中管理全局投屏帧率、清晰度、视频码率和声音开关。
- [x] 默认所有实例跟随全局设置；实例允许切换为单独配置，并可恢复跟随全局。声音也支持相同的全局/实例控制。
- [x] 清晰度控制投屏编码尺寸，不修改安卓内部屏幕分辨率；本轮不通过削减 CPU、内存或暂停应用来省资源。
- [x] 核查 emulator 当前音频启动参数及 scrcpy 音频转发链路，确保开启声音后实际可听（采用 AAC 编码兼容 Android 11+，同时保持宿主 `-no-audio` 避免双路播放）。
- [x] 明确参数生效时机及必要的投屏重开提示，支持不重启安卓实例直接重开投屏并保留窗口位置。
- [x] 默认参数放编译期 [`config/defaults.json`](../config/defaults.json)；用户全局设置和实例覆盖保存在 `VirDeckData/config/settings.json` 内。

### 2. 补齐不明显的失败原因
- [x] 查找操作等待、没有反馈或丢失底层错误的环节，补充当前阶段、合理超时以及进程退出/失败原因。
- [x] 覆盖启动、ADB、环境检测、投屏和工具/镜像下载中的无反馈路径（增加 ADB/环境检测超时与下载无数据 watchdog）。
- [x] 将可定位问题的信息接入现有任务与日志；操作失败时界面能看到失败状态和日志入口，避免无期限等待。

### 3. 基础文件收发
- [x] 提供电脑向安卓实例发送文件、安卓实例向电脑接收/导出文件的入口。
- [x] 复用系统文件选择器及 ADB 传输；限制在 `/sdcard` / `/storage/emulated/0` 共享存储范围内。
- [x] 处理中文、空格、特殊字符文件名及同名冲突，接收前校验符号链接与 Windows 非法文件名，支持传输取消。

### 4. 基础分组
- [x] 支持创建、重命名、删除分组，将实例分配或移动到分组，保留未分组项。
- [x] 按分组筛选实例，并与状态标签页、搜索框及批量操作联动。
- [x] 删除分组不删除实例；分组关系使用稳定实例 ID 保存在 `VirDeckData/config/groups.json`。

---

## 三、后续扩展规划（暂未启动）

### 1. 工作会话（Work Sessions）
- [ ] 软件打开后，从软件内快速打开或恢复对应会话；整合实例按组/按需批量选择与启动、应用或网页直达（`adb shell am start`）、以及已有投屏窗口布局恢复。待上述基础功能交互打磨完成后细化方案。

### 2. 跨平台适配（macOS Apple Silicon / Intel & Linux）
基于现有 `core/` 与 `adapters/` 解耦架构及已预留的 `*_windows.go` / `*_other.go` 构建标签，按以下 5 项推进跨平台支持：

- [ ] **下载器与镜像目录的 OS / CPU 架构动态匹配**（[`core/downloader`](../core/downloader/downloader.go)、[`core/catalog`](../core/catalog/catalog.go)、[`config/defaults.json`](../config/defaults.json)）：
  - `ParseRepositoryXML` 按 `runtime.GOOS`（`windows` / `macosx` / `linux`）与 `runtime.GOARCH` 动态匹配官方组件包，替换当前硬编码的 `host == "windows"`。
  - 系统镜像目录与推荐预设根据宿主架构动态筛选（Apple Silicon `arm64` 自动匹配 `arm64-v8a` 镜像，Intel Mac / Windows / Linux x64 匹配 `x86_64` 镜像），并补充对应平台的 `platform-tools` 与 `scrcpy` 兜底源。
  - ZIP 解压逻辑（`extractZipEntry`）保留 Unix 可执行权限位（`0755`）并支持恢复 macOS `.app` / Framework 内的符号链接（`os.ModeSymlink`）。
- [ ] **纯 Go AVD 配置生成的跨平台与 ARM64 兼容**（[`core/emu/avd_native.go`](../core/emu/avd_native.go)）：
  - 修正 `config.ini` 中 `abi.type` 到 `hw.cpu.arch` 的映射（当镜像为 `arm64-v8a` 时，`hw.cpu.arch` 需写入 `arm64`）。
  - 将 `<Name>.ini` 中硬编码的 `path.rel=avd\%s.avd` 改为使用跨平台路径分隔符。
- [ ] **进程树清理与硬件加速自检适配**（[`core/emu/emu.go`](../core/emu/emu.go)、[`core/emu/proc_other.go`](../core/emu/proc_other.go)）：
  - 非 Windows 平台启动子进程时设置独立进程组（`SysProcAttr{Setpgid: true}`），在 `ForceKillTree` 中通过 `syscall.Kill(-pgid, syscall.SIGKILL)` 清理整个模拟器进程树。
  - 补充 macOS（`sysctl -n kern.hv_support` 检测 `Hypervisor.framework`）与 Linux（`/dev/kvm` 读写权限检测）的前置硬件加速自检与修复指引，并扩展宿主 SDK 默认扫描路径（`~/Library/Android/sdk`、`~/Android/Sdk`）。
- [ ] **多窗口投屏排版跨平台实现**（[`core/screenlayout`](../core/screenlayout/layout.go)）：
  - 第一阶段（轻量通用）：支持在启动或重开 `scrcpy` 时通过 `--window-x`、`--window-y`、`--window-width`、`--window-height` 参数直接传入排版坐标与尺寸。
  - 第二阶段（macOS 原生窗口接管）：新增 `native_darwin.go` 与 `close_darwin.go`，基于 CoreGraphics (`CGWindowListCopyWindowInfo` / `CGGetActiveDisplayList`) 与 Accessibility API (`AXUIElement`) 实现已打开投屏窗口的枚举、动态平铺与布局保存/恢复。
- [ ] **UI 字体回退与 macOS `.app` 数据目录适配**（[`adapters/mygonative/appearance.go`](../adapters/mygonative/appearance.go)、[`core/emu/emu.go`](../core/emu/emu.go)）：
  - 按操作系统设置默认中文字体（Windows 使用 `Microsoft YaHei UI`，macOS 使用 `PingFang SC`，Linux 回退系统无衬线字体）。
  - macOS 打包为 `.app` Bundle 运行时，默认数据目录自动回退至 `~/Library/Application Support/VirDeck`（规避 App Translocation 只读限制，同时保留 `VIRDECK_DATA_DIR` 覆盖能力）。

## 四、明确不纳入当前范围的功能

- 实例完整备份/恢复与磁盘压缩。
- 独立应用管理面板、内置应用商店或桌面快捷方式生成。
- 实时双向文件同步、目录监听或虚拟共享盘系统。
- 游戏按键映射、宏录制或设备指纹深度魔改。

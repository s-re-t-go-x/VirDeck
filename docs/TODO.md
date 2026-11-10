# 当前开发待办 (TODO)

更新日期：2026-11-10

本文档记录当前已完成的基础功能、人性化交互优化与跨平台（macOS Apple Silicon / Intel & Linux）适配清单，以及下一阶段的「工作会话」扩展计划。架构细节与历史归档见 [开发与架构文档](development.md)。

---

## 一、已完成阶段：基础功能的人性化与交互优化（2026-11-10 已全部实现）

针对四项基础功能在日常高频操作中暴露出的路径繁琐、反馈缺失和盲填路径问题，已完成以下四个模块的打磨：

### 1. 文件收发体验闭环
关联代码：[`adapters/mygonative/transfer.go`](../adapters/mygonative/transfer.go)、[`core/service/transfer.go`](../core/service/transfer.go)、[`core/adb/transfer.go`](../core/adb/transfer.go)

- [x] **接通远程目录文件列表（告别盲填路径）**：在「接收到电脑」模式下，利用 [`Manager.ListTransferFiles`](../core/service/transfer.go) 与 [`Client.TransferList`](../core/adb/transfer.go)，在选择常用目录（如 `Download`、`Pictures`、`DCIM`）或切换方向/来源实例后，直接在弹窗内列出该目录下的文件与子文件夹；点击文件选中具体目标，点击文件夹进入子目录，支持「上一级」与手动「读取目录」刷新。
- [x] **优化发送到安卓的目标目录结构与相册刷新**：
  - 发送目录使用带可读时间戳的独立子目录（`YYYYMMDD-HHMMSS-唯一后缀`），同时对同名文件自动编号（`name (2).ext`）防覆盖。
  - 发送完成后自动触发 Android 媒体扫描（`am broadcast -a android.intent.action.MEDIA_SCANNER_SCAN_FILE`），使发送的图片/视频在安卓相册立即可见；刷新失败单独记入日志而不影响文件发送结果。
- [x] **完善本地文件预览、路径记忆与一键打开目录**：
  - 「发送到 Android」选择文件后，在界面展示已选文件名摘要，支持清空或重选。
  - 「接收到电脑」默认将本地接收目录初始化为用户目录下的 `Downloads`，并在本次运行期间记忆上次使用的接收目录。
  - 传输完成后，在弹窗状态区明确展示实际发送目标路径或本地导出文件夹路径，并提供 **「打开接收文件夹」** 按钮（调用 `mygo.Shell.OpenPath`）。

### 2. 投屏与声音的快捷控制及详情栏轻量化
关联代码：[`adapters/mygonative/projection_settings.go`](../adapters/mygonative/projection_settings.go)、[`adapters/mygonative/appearance.go`](../adapters/mygonative/appearance.go)、[`adapters/mygonative/app.go`](../adapters/mygonative/app.go)

- [x] **增加场景化一键预设档位**：在全局「设置 → 投屏与声音」及单实例「投屏设置」表单顶部，提供三档快捷预设按钮，点击即可自动填充画面参数并保留当前声音选择：
  - **省资源多开**：`15 FPS · 最长边 960 px · 2 Mbps`（适合后台多开挂机巡检）
  - **均衡日常（默认）**：`60 FPS · 最长边 1280 px · 8 Mbps`
  - **高清流畅**：`60 FPS · 最长边 1920 px · 16 Mbps`
- [x] **详情栏轻量化摘要与顶部「静音 / 有声」一键切换**：
  - 将投屏配置状态收敛为右侧详情栏「设备配置」中的单行「投屏配置」摘要（如 `默认 · 60 FPS · 1280px · 静音` 或 `独立 · 15 FPS · 960px · 开启声音`），并在详情栏顶部主操作栏旁提供紧凑的「静音 / 有声」快捷按钮。
  - 点击快捷按钮立即切换该实例声音状态（当切换后参数与全局设置一致时自动恢复跟随全局），且仅对当前已打开投屏的实例自动重开投屏。
- [x] **支持批量投屏设置与全局保存后一键重开**：
  - 多选实例时开放「投屏设置」，支持将多台实例批量设为同一预设或批量恢复「跟随全局设置」（所选实例配置不一致时需明确选择统一方式）。
  - 在全局「设置 → 投屏与声音」中增加 **「保存并重开已打开投屏」** 按钮，仅重开当前跟随全局且未忙碌的已有投屏窗口。

### 3. 分组归属可视化与管理交互优化
关联代码：[`adapters/mygonative/groups.go`](../adapters/mygonative/groups.go)、[`adapters/mygonative/app.go`](../adapters/mygonative/app.go)

- [x] **在实例列表与详情栏头部轻量展示所属分组**：
  - 当处于「全部分组」视图时，在实例列表行副标题中将所属分组名与系统镜像同行展示（如 `工作 · Android 11 · Play`），保持两行紧凑行高。
  - 在右侧详情栏头部状态徽章旁提供轻量分组胶囊菜单（`分组: xxx ▾`），支持直接切换分组或点击「+ 新建分组并移入」。
- [x] **分组筛选器显示实例计数**：顶部分组下拉框（`全部分组`、`未分组` 及各自定义分组）旁附带显示对应的总实例数量（如 `工作 (3)`、`未分组 (1)`），不受状态与搜索过滤影响。
- [x] **重构「管理分组」弹窗与新建/克隆归组流程**：
  - 「管理分组」弹窗采用「已有分组列表（显示成员数、重命名、删除）+ 独立新建分组输入区」结构，并在创建、改名、删除后给出明确的状态提示。
  - 在「新建实例」与「克隆实例」弹窗中支持选择「所属分组」（新建默认继承当前筛选分组，克隆默认继承源实例所属分组）。

### 4. 错误反馈触达与日志可复制性
关联代码：[`adapters/mygonative/app.go`](../adapters/mygonative/app.go)、[`adapters/mygonative/tasks.go`](../adapters/mygonative/tasks.go)

- [x] **窄窗口与列表行的异常原因直达**：
  - 为实例列表行的 `✖ 异常` 状态绑定 `Tooltip(inst.LastError)` 悬浮提示，并在状态下方展示单行简短错误摘要；详情栏「最近错误」置于主操作区正下方，无需滚动即可首屏查看。
  - 移除底部任务栏「查看失败原因」按钮在窄窗口（`< 900px`）下的隐藏限制，确保任何窗口尺寸下任务失败都能一键展开定位。
- [x] **启动中阶段耗时反馈**：实例处于 `◐ 启动中` 时，动态显示已等待秒数（`◐ 启动中 · 18s`）及当前引导阶段提示（列表行「等待桌面就绪」、详情栏「正在引导 Android 系统，等待桌面就绪…」）。
- [x] **支持一键复制日志与错误信息**：在底部「运行日志」面板增加 **「复制日志」** 按钮，并在实例详情的「最近错误」区域增加 **「复制错误」** 按钮，一键写入系统剪贴板。

---

## 二、已完成阶段：四项基础功能（2026-11-09 已交付）

### 1. 省资源与投屏参数
- [x] 在设置界面集中管理全局投屏帧率、清晰度、视频码率和声音开关。
- [x] 默认所有实例跟随全局设置；实例允许切换为单独配置，并可恢复跟随全局。声音也支持相同的全局/实例控制。
- [x] 清晰度控制投屏编码尺寸，不修改安卓内部屏幕分辨率；不通过削减 CPU、内存或暂停应用来省资源。
- [x] 核查 emulator 音频启动参数及 scrcpy 音频转发链路，确保开启声音后实际可听（采用 AAC 编码兼容 Android 11+，同时保持宿主 `-no-audio` 避免双路播放）。
- [x] 明确参数生效时机及投屏重开提示，支持不重启安卓实例直接重开投屏并保留窗口位置。
- [x] 默认参数放编译期 [`config/defaults.json`](../config/defaults.json)；用户全局设置和实例覆盖保存在 `VirDeckData/config/settings.json` 内。

### 2. 补齐不明显的失败原因
- [x] 查找操作等待、没有反馈或丢失底层错误的环节，补充当前阶段、合理超时以及进程退出/失败原因。
- [x] 覆盖启动、ADB、环境检测、投屏和工具/镜像下载中的无反馈路径（增加 ADB/环境检测超时与下载无数据 watchdog）。
- [x] 将可定位问题的信息接入现有任务与日志；操作失败时界面能看到失败状态和日志入口，避免无期限等待。

### 3. 基础文件收发
- [x] 提供电脑向安卓实例发送文件、安卓实例向电脑接收/导出文件的入口。
- [x] 复用系统文件选择器及 ADB 传输；限制在 `/sdcard` / `/storage/emulated/0` 共享存储范围内。
- [x] 处理中文、空格、特殊字符文件名及同名冲突，接收前校验符号链接与非法文件名，支持传输取消。

### 4. 基础分组
- [x] 支持创建、重命名、删除分组，将实例分配或移动到分组，保留未分组项。
- [x] 按分组筛选实例，并与状态标签页、搜索框及批量操作联动。
- [x] 删除分组不删除实例；分组关系使用稳定实例 ID 保存在 `VirDeckData/config/groups.json`。

---

## 三、已完成阶段：跨平台适配（macOS Apple Silicon / Intel & Linux，2026-11-10 已全部实现）

基于现有 `core/` 与 `adapters/` 解耦架构及 `*_windows.go` / `*_darwin.go` / `*_other.go` 构建标签，已完成以下 5 项跨平台支持与多平台打包检查：

- [x] **下载器与镜像目录的 OS / CPU 架构动态匹配**（[`core/downloader`](../core/downloader/downloader.go)、[`core/catalog`](../core/catalog/catalog.go)、[`config/defaults.json`](../config/defaults.json)）：
  - `ParseRepositoryXML` / `ParseRepositoryXMLForHost` 按 `runtime.GOOS`（`windows` / `macosx` / `linux`）与 `runtime.GOARCH` 动态匹配官方组件包，替换硬编码的 `host == "windows"`。
  - 系统镜像目录与推荐预设根据宿主架构动态筛选（Apple Silicon `arm64` 自动匹配 `arm64-v8a` 镜像，Intel Mac / Windows / Linux x64 匹配 `x86_64` 镜像），并补充对应平台的 `platform-tools` 与 `scrcpy`（`v5.0.1`）兜底源。
  - 解压与离线导入逻辑（`ExtractArchiveAtomically` / `ExtractZipAtomically`）支持 `.zip` 与 `.tar.gz` / `.tgz`，保留 Unix 可执行权限位（`0755`），支持恢复 macOS `.app` / Framework 内的符号链接（`os.ModeSymlink`）并做全量符号链接防越界预检；空间清理同步支持清理 `.tar.gz` / `.tgz` 下载包。
- [x] **纯 Go AVD 配置生成的跨平台与 ARM64 兼容**（[`core/emu/avd_native.go`](../core/emu/avd_native.go)、[`core/emu/emu.go`](../core/emu/emu.go)）：
  - 修正 `config.ini` 中 `abi.type` 到 `hw.cpu.arch` 的映射（当镜像为 `arm64-v8a` 时，`hw.cpu.arch` 写入 `arm64`），并在 `EnsureSystemImageMetadata` 中区分 ARM 与 Intel 镜像描述。
  - 将 `CreateAVDNative`、`CloneAVD` 与 `HealAVDIniPaths` 中 `<Name>.ini` 的 `path.rel` 统一改为使用 `filepath.Join("avd", name+".avd")` 跨平台路径分隔符。
- [x] **进程树清理与硬件加速自检适配**（[`core/emu/emu.go`](../core/emu/emu.go)、[`core/emu/proc_other.go`](../core/emu/proc_other.go)）：
  - 非 Windows 平台启动子进程时设置独立进程组（`SysProcAttr{Setpgid: true, Pgid: 0}`），在 `ForceKillTree` 中校验进程组归属并通过 `syscall.Kill(-pgid, syscall.SIGKILL)` 清理整个模拟器进程树。
  - 补充 macOS（`sysctl -n kern.hv_support` 检测 `Hypervisor.framework`）与 Linux（`/dev/kvm` 读写权限检测）的前置硬件加速自检与修复指引，放开非 Windows 平台的 UTF-8 数据目录路径检查，并扩展宿主 SDK 默认扫描路径（`~/Library/Android/sdk`、`~/Android/Sdk`）。
- [x] **多窗口投屏排版跨平台实现**（[`core/screenlayout`](../core/screenlayout/layout.go)、[`core/emu/projection_portable.go`](../core/emu/projection_portable.go)）：
  - 第一阶段（轻量通用）：支持在启动或重开 `scrcpy` 时通过 `--window-x`、`--window-y`、`--window-width`、`--window-height` 参数（及 CLI `screen -x -y -width -height`）直接传入排版坐标与尺寸，并在不支持原生窗口枚举的宿主（如 Linux）上自动回退进程级投屏跟踪。
  - 第二阶段（macOS 原生窗口接管）：新增 [`native_darwin.go`](../core/screenlayout/native_darwin.go) 与 [`close_darwin.go`](../core/screenlayout/close_darwin.go)，基于 CoreGraphics (`CGWindowListCopyWindowInfo` / `CGGetActiveDisplayList`)、AppKit (`NSScreen`) 与 Accessibility API (`AXUIElement`) 实现已打开投屏窗口的枚举、动态平铺、关闭与布局保存/恢复。
- [x] **UI 字体回退、macOS `.app` 数据目录适配与多平台打包**（[`adapters/mygonative/appearance.go`](../adapters/mygonative/appearance.go)、[`core/emu/emu.go`](../core/emu/emu.go)、[`scripts/package/main.go`](../scripts/package/main.go)）：
  - 按操作系统设置默认中文字体（Windows 使用 `Microsoft YaHei UI`，macOS 使用 `PingFang SC`，Linux 回退系统 `sans-serif`），并在文件传输等界面文案中动态显示宿主系统名称（Windows / macOS / Linux）。
  - macOS 打包为 `.app` Bundle 运行时，默认数据目录自动回退至 `~/Library/Application Support/VirDeck`（规避 App Translocation 只读限制，同时保留 `VIRDECK_DATA_DIR` 覆盖能力），并提供 [`scripts/package`](../scripts/package/main.go) 与 GitHub Actions 跨平台构建工作流（[`.github/workflows/cross-platform.yml`](../.github/workflows/cross-platform.yml)）。

---

## 四、后续扩展规划（暂未启动）

### 1. 工作会话（Work Sessions）
- [ ] 软件打开后，从软件内快速打开或恢复对应会话；整合实例按组/按需批量选择与启动、应用或网页直达（`adb shell am start`）、以及已有投屏窗口布局恢复。

### 2. 机型一键伪装（Device Model Spoofing）
- [ ] **机型预设与参数注入**：提供主流机型模板（如小米、三星、ROG 等热门机型及平板预设），支持单实例与批量随机/自选伪装。
- [ ] **系统属性与硬件配置同步**：启动时通过官方 `emulator -prop` 注入系统参数（`ro.product.model`、`ro.product.brand`、`ro.product.manufacturer` 等），并在 AVD `config.ini` 中同步更新对应硬件特征。
- [ ] **独立设备标识重置**：支持一键生成或随机重置每台实例独立的 `ANDROID_ID` (SSAID) 与虚拟硬件标识。

### 3. ARM 应用兼容性（官方转译镜像筛选与标注）
- [ ] **内置转译镜像识别与标注**：在系统镜像列表与官方同步仓库中，自动识别并标注内置 ChromeOS `libndk_translation` / ARM 转译层的官方镜像（如指定 Android 11+ Google APIs x86_64 镜像），提供清晰的「支持 ARM 应用」角标与筛选维度。
- [ ] **创建实例引导提示**：在创建实例与推荐镜像面板中提供友好的引导提示，方便 x86 宿主用户在需要运行纯 ARM native so 库应用时快速选用兼容镜像。

### 4. 文件传输操作逻辑重新设计

### 5. 安全加固与完整性校验（下一小版本优先 · 采纳 SEC-09 与 SEC-10）
详见完整审核报告：[安全审核与加固报告](security-audit-report.md)

- [ ] **组件下载支持 SHA-256 自适应校验（SEC-09）**：
  - 在 [`core/downloader/downloader.go`](../core/downloader/downloader.go) 中扩展哈希校验：自动根据哈希字符串长度识别 SHA-256（64 位 hex）与 SHA-1（40 位 hex），实现自适应校验与断点续传验证。
  - 为 [`config/defaults.json`](../config/defaults.json) 中预设的 `scrcpy` 跨平台发布包（`scrcpy-win64`、`scrcpy-macos`、`scrcpy-linux`）及推荐系统镜像补充官方固定的 SHA-256 校验和。
  - 在自定义镜像或无哈希下载任务的 UI 处增加醒目的「未提供安全校验和」提示，避免无验证直接解压。
- [ ] **依赖版本更新与供应链加固（SEC-10 & SEC-11）**：
  - 在下一次外网环境发版或依赖常规升级时，执行 `go get golang.org/x/net@v0.62.0` 并运行 `go mod tidy`。
  - 将 [`.github/workflows/cross-platform.yml`](../.github/workflows/cross-platform.yml) 中引用的第三方 Action（如 `softprops/action-gh-release`、`actions/checkout`）锁定为完整 40 位 Git Commit SHA。

---

## 五、明确不纳入当前范围的功能

- 实例完整备份/恢复与磁盘压缩。
- 独立应用管理面板、内置应用商店或桌面快捷方式生成。
- 实时双向文件同步、目录监听或虚拟共享盘系统。
- 游戏按键映射、连招宏录制。
- 深度内核/Hypervisor 源码级反作弊对抗（保持官方二进制轻量架构与单目录纯净自包含，不自研魔改 QEMU/内核）。

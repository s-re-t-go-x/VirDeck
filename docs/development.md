# VirDeck 开发与架构文档

本文档面向开发者与维护者，说明 VirDeck (`v0.10.0`) 的模块分层、核心技术方案、跨平台实现机制、配置与构建发布规范，以及各阶段演进归档。用户操作说明请见 [使用指南 (usage.md)](usage.md)，安全加固详情请见 [安全审核与加固报告 (security-audit-report.md)](security-audit-report.md)。

---

## 一、架构总览与模块职责

项目严格遵循 **「核心逻辑 (`core`) 与适配层 (`adapters`) 单向依赖」** 原则：`core/` 不依赖任何 GUI 库，桌面端 UI 与 CLI 均仅通过 [`service.Service`](../core/service/types.go) 接口与核心层交互。

```text
cmd/
 ├── app/                 # 桌面端入口（带参数时支持 `VirDeck cli ...` 或 `__socks-bridge` 辅助进程）
 └── cli/                 # 独立命令行可执行文件入口
adapters/
 ├── mygonative/          # MyGo 原生 GPU 自绘桌面界面（5 大主视图壳层、悬浮任务胶囊、弹窗与状态机）
 │   ├── shell.go         # 左侧导航栏、内嵌分组树、窄屏顶栏与页面路由
 │   ├── page_workbench.go# 实例工作台、分层操作条、详情白卡与元素级拖放
 │   ├── page_images.go   # 系统镜像目录、运行环境自检、缺失决策卡与空间清理
 │   ├── page_logs.go     # 独立运行日志整页（筛选/搜索/复制）
 │   ├── page_settings.go # 全局设置（常规与下载 / 投屏与声音）
 │   ├── install_modal.go # 统一安装流水线确认卡与拖放分流
 │   ├── syncinput.go     # 多设备同步配置弹窗与工作台同步条
 │   ├── tools_modals.go  # 虚拟定位、应用管理、一键截图、磁盘清理与备份还原 UI
 │   └── task_overlay.go  # 右下角悬浮任务胶囊、多阶段步骤卡片与 Toast
 └── cli/                 # 命令行参数解析、表格输出与独立 SOCKS5 桥接入口
core/
 ├── service/             # 业务编排层：实例生命周期、安装流水线、同步控制、运维工具、分组与排版
 ├── instance/            # 实例注册表（ID/内部 AVD 名/显示名分离、ABI/最近应用缓存、JSON 原子持久化）
 ├── emu/                 # 纯 Go AVD 生成、路径自愈、Windows 短路径/Junction 映射、SOCKS5 桥接、磁盘压缩备份与投屏
 ├── screenlayout/        # Win32 / macOS 原生窗口发现与控制、显示器工作区枚举、固定比例平铺 Pack 算法
 ├── adb/                 # ADB 封装、引导/pm 探活、安装错因翻译、Shell、定位/截图/应用管理与安全文件传输
 ├── apk/                 # 纯 Go APK/APKS/XAPK 二进制 AXML 解析、ABI/DPI 分包筛选与兼容性检查
 ├── catalog/             # 三级系统镜像目录（宿主架构推荐、ARM 兼容分类、官方 XML 同步与自定义/本地扫描）
 ├── downloader/          # 断点续传下载器（Range + If-Range、空闲超时 watchdog、SHA-256/SHA-1 校验、安全解压）
 └── syncinput/           # Windows 全局鼠标钩子捕获、归一化坐标映射与 scrcpy-server 纯控制通道协议
config/
 ├── defaults.json        # 编译期内嵌初始默认配置（通过 go:embed 打包）
 ├── defaults.go          # 默认配置解析、按宿主 OS/Arch 动态派生 (ForHost) 与校验
 └── redact.go            # 代理凭据脱敏 (RedactProxyURL / RedactError)
assets/                   # 产品元数据 (product.json)、GUI/CLI 图标与 Windows 资源 (.syso)
scripts/
 ├── icongen/             # 纯 Go 图标 (.ico) 与 Windows 版本资源 (.syso) 生成工具
 └── package/             # 跨平台二进制打包工具（生成 Windows / macOS .app / Linux 可执行文件）
.github/workflows/        # 跨平台静态检查、测试与手动触发 Release 工作流 (cross-platform.yml)
```

---

## 二、核心技术机制详解

### 1. 免 JDK 纯 Go AVD 引擎与运行稳定性 (`core/emu` & `core/service`)
- **纯 Go 生成 AVD 与配置**：创建实例时直接生成 `<AVDHome>/<AVDName>.ini` 与 `<AVDName>.avd/config.ini`，支持 `4–128 GB` 数据分区（`disk.dataPartition.size`）、`720p` / `1080p` / 平板横屏 / 自定义宽高与 DPI、手动 GPU 渲染模式、动态映射 `hw.cpu.arch`（`x86_64` / `x86` / `arm64`）及自动补齐 `package.xml`。
- **安全清锁与 GPU 早期秒退回退 (`core/emu/stability.go`)**：
  - 启动前通过 `adb` 确认无同 AVD 存活进程后，再调用 `CleanStaleLocks` 清理残留 `.avd/*.lock`，避免破坏 `Manager.Reconcile` 的孤儿接管。
  - 若模拟器在启动后 `< 10s` 内异常退出且 stderr 命中图形/OpenGL/Vulkan 初始化错误（`IsGPUStartupFailure`），沿平台回退链（`FallbackGPUMode`）自动重试并持久化有效 `GPUMode`。
- **可选快速启动（快照）与冷启动自愈**：默认保持冷启动（`-no-snapshot`）；单实例启用 `QuickBoot` 时支持快照启动，若检测到硬件/代理/分辨率变更或上次快速启动崩溃/强杀，自动附加 `-no-snapshot-load` 执行冷启动自愈，并提供 `ClearInstanceSnapshot` 手动清除快照。
- **事件通道分离与 `WaitInstanceReady`**：`eventSubscriber` 将 `EventStateChanged` 放入独立优先队列，避免高频日志/下载事件挤占丢弃状态变更；`WaitInstanceReady` 采用「先 `Subscribe()` → 查表 → 监听 + `1.5s` 轮询兜底」，且不跨步持有 `maintenance.RLock()`；`vm.busy` 关联 `context.CancelFunc`，允许 `actStop` 随时中断等待并停机。

### 2. 数据目录隔离、Windows 中文路径绕过与代理桥接 (`core/emu`)
- **自适应数据目录 (`DefaultDataDir`)**：便携模式使用同级 `VirDeckData/`；macOS 从 `.app` Bundle 启动默认回退至 `~/Library/Application Support/VirDeck`；支持 `VIRDECK_DATA_DIR`（数据目录）、`VIRDECK_REGISTRY`（实例注册表路径）环境变量或启动指针文件（`virdeck_datadir.txt` / 用户配置目录 `data_dir.txt`）覆盖。
- **四大环境变量隔离**：拉起 `emulator` 与 `adb` 时显式注入隔离环境，与宿主可能存在的 Android Studio 环境彻底解耦：
  - `ANDROID_SDK_ROOT` / `ANDROID_HOME` → `<DataDir>/sdk`（本地未安装时回退扫描宿主 `ANDROID_SDK_ROOT`、macOS `~/Library/Android/sdk` 或 Linux `~/Android/Sdk`）
  - `ANDROID_AVD_HOME` → `<DataDir>/avd`
  - `ANDROID_USER_HOME` / `ANDROID_EMULATOR_HOME` → `<DataDir>/.android`
  - `ANDROID_ADB_SERVER_PORT=5137`（与宿主默认 `5037` 端口隔离）
- **Windows 中文路径透明映射 (`winpath_windows.go`)**：当数据目录包含非 ASCII 字符时，依次尝试 Win32 `GetShortPathNameW`（8.3 短路径）与非管理员 NTFS 目录联接（`%PUBLIC%\VirDeckLinks\d_<hash>`）为官方 `emulator.exe` 提供纯 ASCII 运行映射；若均受限则通过 UI 引导「一键改用纯英文数据目录」（`SaveDataDirOverride`）。
- **HTTP / SOCKS5 代理桥接与凭据脱敏 (`socks_bridge.go` & `config/redact.go`)**：
  - 移除代理模式下的硬编码 `-dns-server 8.8.8.8,1.1.1.1`，默认跟随宿主 DNS。
  - 当实例配置 `socks5://` / `socks5h://` 时，桌面端拉起进程内 `127.0.0.1:0` 本地 HTTP CONNECT → SOCKS5 TCP/HTTP 桥接；CLI 模式通过 `StartDetachedSOCKSBridge` 拉起独立 `__socks-bridge -watch-port <port>` 守护进程并在模拟器端口关闭后自动退出。
  - 所有日志、CLI 列表与 UI 错误均经 `config.RedactProxyURL` / `config.RedactError` 将密码脱敏为 `***`。

### 3. 统一安装流水线与 APK/APKS/XAPK 预检 (`core/apk` & `core/service/install.go`)
- **纯 Go 轻量包解析 (`core/apk`)**：仅用标准库 `archive/zip` 与 `encoding/binary` 解析二进制 `AndroidManifest.xml`（AXML）包名与 `lib/<abi>/` 原生库，无需外部 `aapt2`；对 `.apks` / `.xapk` 分包实施 128 条目与 2 GiB 解压总体积安全上限，并按实例运行态 `ro.product.cpu.abilist`（开机首次探测缓存）与目标 DPI 筛选一致的 `SelectSplitAPKs` 集合调用 `adb install-multiple`。
- **端到端流水线 (`InstallApp`)**：自动启动停机实例 → `WaitInstanceReady` → `pm path android` 就绪探活 → `adb install -r [-g]`（含 `TranslateInstallError` 中文错误映射）→ 设备端 `cmd package resolve-activity --brief -c android.intent.category.LAUNCHER <pkg>` 解析入口并以 `am start -n` 拉起（弃用 `monkey`）→ 仅 `Headless == true` 时自动打开 `scrcpy` → 写入 `RecentApps`。
- **防穿透元素级拖放**：UI 弃用全窗 `win.OnFileDrop`，改为在 `page_workbench.go` 使用容器/行级 `FileDragOver()` 与 `DroppedFiles()` 且仅在 `!anyModalOpen()` 时生效。

### 4. 多窗口投屏排版与多设备输入同步 (`core/screenlayout` & `core/syncinput`)
- **三层跨平台窗口排版**：Windows Win32 API、macOS `purego`（`CoreGraphics` / `NSScreen` / `AXUIElement`）与 Linux `scrcpy` 启动坐标回退；支持每行窗口数（`Columns`）、记忆各实例窗口位置（`RememberPosition`）及按分组存储多套 `NamedScreenLayout`（`config/screen_layout.json`）。
- **多设备操作同步 (`core/syncinput`)**：
  - Windows 端使用非管理员 `WH_MOUSE_LL` 钩子捕获主控 `scrcpy` 窗口客户区鼠标事件（~60Hz 节流）及 `Ctrl+Alt+P` 热键，扣除标题栏/黑边/DPI 后归一化映射，离开主控区或失焦时自动向被控机补发 `ACTION_UP` 防断触卡死。
  - 控制通道直接向被控实例推送内置 `scrcpy-server`（`video=false audio=false control=true`），经 `adb forward` 发送二进制控制报文（触控、滚轮、系统按键与 `SET_CLIPBOARD + paste=true` Unicode/中文粘贴），支持无头被控机与防齐步随机延迟/位置微偏移。

### 5. 镜像目录、安全下载与磁盘/备份工具 (`core/catalog`、`core/downloader` & `core/emu/disk.go`)
- **ARM 兼容分类 (`core/catalog/arm.go`)**：自动识别 ARM64 原生镜像与 API 30+ `google_apis` / `google_apis_playstore` `x86_64` 镜像的 `libndk_translation` 转译支持，提供 UI 筛选胶囊与装包前 ABI 兼容性检验。
- **自适应校验与安全解压 (`core/downloader`)**：根据校验和长度自适应验证 SHA-256（64 位 hex）与 SHA-1（40 位 hex）；支持 `.zip` 与 `.tar.gz` / `.tgz` 原子解压，保留 Unix 可执行权限位与安全的框架符号链接，采用 APFS/HFS+ 大小写折叠与 NFD 归一化键阻断 Zip Slip 与符号链接越界写入；支持「安装成功后自动删除下载包」。
- **磁盘清理与便携备份/还原 (`core/emu/disk.go` & `core/service/backup_disk.go`)**：运行中通过 `adb shell sm fstrim` 整理空间；停机状态移除 `cache.img.qcow2` 并调用 `qemu-img convert -O qcow2 -B <backing>` 压缩覆盖层镜像；单实例备份与还原采用原子写 `.zip` 并校验禁止 `.zip` 内符号链接及路径穿越。

---

## 三、配置体系与构建发布规范

### 1. 编译期默认配置 vs 运行时用户配置
- **[`config/defaults.json`](../config/defaults.json)**：通过 `go:embed` 内嵌为初始默认值，由 `config.ForHost(runtime.GOOS, runtime.GOARCH)` 派生当前宿主源与兼容架构镜像预设；运行时不读取外部 `defaults.json`。
- **默认收藏增量迁移**：`favoriteVersion` 当前至版本 `4`（保留 `Android 7.1 / 9`，置顶 `Android 11 / 14` x86_64 与 ARM64 镜像）。

### 2. 跨平台打包与编译命令
- **开发环境要求**：Go `1.27.4`（以 [`go.mod`](../go.mod) 为准），纯 Go 架构（`CGO_ENABLED=0`）。

推荐使用 [`scripts/package`](../scripts/package/main.go) 构建各平台可执行二进制（自动注入 `CGO_ENABLED=0` 与生产构建标志）：

```powershell
# 构建当前平台可执行程序（产出 dist/VirDeck-v<版本>-<系统>-<架构> 及标准名副本）
go run ./scripts/package

# 交叉构建指定目标平台（支持 windows/amd64、darwin/arm64、darwin/amd64、linux/amd64）
go run ./scripts/package -os darwin -arch arm64
go run ./scripts/package -os darwin -arch amd64
go run ./scripts/package -os linux -arch amd64
```

也可以在项目根目录直接手动编译可执行文件（以 Windows 为例）：

```powershell
# 编译桌面端 GUI（无控制台黑框）
go build -trimpath -tags mygo_noinspector -ldflags '-H=windowsgui -s -w -X github.com/egoist/mygo.production=1' -o dist/VirDeck.exe ./cmd/app

# 编译命令行工具 CLI
go build -trimpath -ldflags '-s -w' -o dist/virdeck-cli.exe ./cmd/cli
```

若修改了 [`assets/product.json`](../assets/product.json) 或图标，先运行 `go generate ./assets` 更新 Windows `.syso` 资源文件（`TestWindowsSysoMatchesProductVersion` 会校验 `.syso` 内嵌版本与 `product.json` 一致）。

### 3. 跨平台 CI 与 GitHub Release 发布
工作流配置在 [`.github/workflows/cross-platform.yml`](../.github/workflows/cross-platform.yml)，所有第三方 Action 均锁定 40 位 Commit SHA，采用纯手动触发（`workflow_dispatch`）：
1. **前置条件**：在源仓库打好与 [`assets/product.json`](../assets/product.json) 一致的 Tag（如 `v0.10.0`）并同步镜像至 GitHub。
2. **手动触发**：进入 GitHub **Actions** → **Cross-platform checks & Release** → **Run workflow**，选择是否发布 Release、标签名（留空自动读取 `assets/product.json`）及是否标记为 Pre-release。
3. **执行流程**：`prepare`（校验 Tag 与防覆盖已有 Release）→ `native`（在 `windows-latest`、`macos-latest`、`ubuntu-latest` 分别运行 `go vet ./...`、`go test ./... -count=1` 与 `scripts/package` 构建，macOS 同时构建 `arm64` 与 `amd64`）→ `release`（汇总全平台制品并调用 `gh release create --verify-tag` 发布）。

---

## 四、测试与验证指南

```powershell
# 运行常规静态检查与单元测试
go vet ./...
go test ./... -count=1

# 跨平台静态检查（PowerShell 示例）
$env:GOOS='darwin'; $env:GOARCH='arm64'; $env:CGO_ENABLED='0'; go vet ./...
```

> **说明**：`core/catalog`、`core/downloader`、`core/emu`、`core/service` 中的本地回环 HTTP/SOCKS5 测试与 Windows NTFS 目录联接测试需在允许本地回环监听及目录联接的环境中执行；Linux 无头环境下 UI 测试需配合 `xvfb-run -a` 运行。`core/screenlayout` 另提供可选原生窗口冒烟测试（`VIRDECK_NATIVE_SCREENLAYOUT_TEST=1`）。

---

## 五、里程碑演进记录

- **M1–M4（2026-11-05 ～ 2026-11-06）**：完成核心分层骨架、纯 Go 免 JDK AVD 引擎、`VirDeckData/` 目录隔离与路径自愈、断点续传下载器与三级镜像目录。
- **设备工作台 UI 重构与基础功能交付（2026-11-06 ～ 2026-11-10）**：完成三栏工作台、多窗口投屏排版、全局/单实例投屏与 AAC 声音控制、安全 `/sdcard` 文件收发与远程目录浏览、非破坏式分组、启动计时与日志复制。
- **跨平台适配与安全加固（2026-11-10）**：完成 `windows/amd64`、`darwin/arm64`、`darwin/amd64` 与 `linux/amd64` 架构匹配、`.tar.gz` 符号链接安全解压、Unix 进程组清理、macOS `purego` 原生窗口排版与 `.app` 适配、以及 GitHub Actions 跨平台发布流程。
- **v0.10.0 全面升级与实用工具收口（2026-11-24 ～ 2026-11-27）**：
  - 安全与稳定性加固：落地 SHA-256 自适应校验、缺失校验和 UI 预警、下载后自动删包、代理凭据脱敏与去硬编码 DNS、SOCKS5 本地 HTTP CONNECT 桥接、Windows 中文路径 `8.3`/Junction 透明映射、安全清锁与 GPU 早退回退、状态事件优先队列、`QuickBoot` 快照开关与冷启动自愈、依赖升级（`mygo v0.10.0`、`golang.org/x/net v0.62.0`）及 CI Action SHA 锁定。
  - 5 大主视图 UI 重构与功能闭环：落地「冷雾灰 · 深靛蓝」主题与右下角悬浮任务胶囊、整页化文件传输/镜像与环境/运行日志/设置、移除旧版克隆入口、打通统一 `.apk/.apks/.xapk` 安装与防穿透拖放流水线、ARM 兼容标注与运行态 `abilist` 探测、分组错峰启动与分组命名布局、Windows 主控多设备鼠标同步与全平台 Unicode 剪贴板广播、以及虚拟定位、无头截图、应用管理、磁盘清理与单实例 `.zip` 便携备份还原。

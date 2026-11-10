# VirDeck

**VirDeck** 是一款支持 **Windows 10 / 11 (x64)**、**macOS (Apple Silicon / Intel)** 与 **Linux (x64)** 的轻量、自包含安卓多实例桌面管理器。底层基于官方 Android Emulator、ADB 与 scrcpy，采用 **Go + MyGo 原生 GPU UI** 构建，**无需安装 Android Studio 或 Java / JDK**，所有运行组件、系统镜像与实例数据统一收敛在单数据目录（便携模式 `VirDeckData/`，macOS `.app` 默认 `~/Library/Application Support/VirDeck`）中，支持开箱即用与便携迁移。

---

## 核心特性

- **零 JDK 依赖（纯 Go 引擎）**：直接用 Go 生成 AVD `.ini`、`config.ini` 与符合 Android SDK Schema 的 `package.xml`，彻底省去 200MB+ 的 JDK 与 `avdmanager` / `sdkmanager` 依赖。
- **单目录收敛与便携迁移**：配置、SDK 工具（`emulator`、`adb`）、`scrcpy`、系统镜像与虚拟机磁盘全部存放在独立数据目录；内置四大环境变量隔离与独立 ADB 端口（默认 `5137`），移动文件夹后启动自动修复 `.ini` 路径。
- **内置断点续传下载与三级镜像管理**：支持按宿主 OS / CPU 架构动态匹配并一键在线下载运行工具与系统镜像（HTTP `Range` + `If-Range` 断点续传、强制 `SHA-1` 校验、防目录穿越与防符号链接越界原子解压），同时支持同步 Google 官方镜像源、星标收藏备注、自定义镜像源及离线 `.zip` / `.tar.gz` 导入。
- **批量多开、分组与数据隔离**：一键批量创建拥有独立 `userdata` 与设备标识（SSAID / GSF ID）的干净实例；支持分组管理与筛选、批量启停、批量安装 APK、批量执行 ADB Shell 命令及逐实例独立 HTTP 代理。
- **无头省资源运行 + 独立投屏与多窗口排版**：实例默认以冷启动（`-no-snapshot`）和后台无窗口模式（`-no-window`）运行；按需通过 `scrcpy` 打开画面，支持全局/单实例帧率、清晰度、码率、三档场景预设与声音（AAC）快捷控制，并提供多窗口一键平铺、基准窗口对齐、新投屏自动补位及布局保存/恢复（Windows Win32 与 macOS 原生窗口接管，Linux 支持启动坐标排版）。
- **安全文件收发与空间清理**：提供宿主电脑（Windows / macOS / Linux）与 Android 共享存储之间的文件发送与远程目录可视浏览导出，内置路径越界与文件名冲突预检、发送后自动刷新安卓相册；支持一键扫描清理已下载压缩包（`.zip` / `.tar.gz`）、未完成缓存及已停止实例的 AVD 缓存分区。
- **桌面端 GUI + CLI 双入口**：提供直观的「设备工作台」桌面界面与完整的 `virdeck-cli` 命令行工具，便于日常手动操作或脚本自动化集成。

---

## 系统要求

- **操作系统**：Windows 10 / 11 (x86_64)、macOS (Apple Silicon `arm64` / Intel `x86_64`)、Linux (x86_64)
- **硬件虚拟化**：
  - **Windows**：BIOS 开启虚拟化（Intel VT-x / AMD-V），并在「启用或关闭 Windows 功能」中勾选 **Windows 虚拟机监控程序平台 (Windows Hypervisor Platform, WHPX)**
  - **macOS**：支持 `Hypervisor.framework`（`sysctl -n kern.hv_support` 为 `1`）；多窗口投屏排版需在「系统设置 → 隐私与安全性 → 辅助功能」中授权 VirDeck
  - **Linux**：BIOS 开启虚拟化，加载 KVM 内核模块且当前用户具备 `/dev/kvm` 读写权限
- **目录路径要求**：Windows 下程序与 `VirDeckData/` 所在路径需为**纯英文/数字（ASCII）且具备读写权限**（Windows 版官方 Android Emulator 底层不支持含中文字符的路径；macOS 与 Linux 支持 UTF-8 路径）
- **开发环境（仅源码编译需要）**：Go `1.27.3`（以 [`go.mod`](go.mod) 为准）

---

## 快速开始

### 1. 编译或打包可执行文件

可使用内置跨平台打包脚本一键构建当前平台（或指定 `-os` / `-arch`）的发行压缩包：

```powershell
# 构建当前宿主平台的开发发行包（输出至 dist/VirDeck-<os>-<arch>.zip）
go run ./scripts/package

# 或跨平台指定目标构建（支持 windows/amd64、darwin/arm64、darwin/amd64、linux/amd64）
go run ./scripts/package -os darwin -arch arm64
```

也可以在项目根目录直接编译可执行文件（以 Windows 为例）：

```powershell
# 编译桌面端 GUI（无控制台黑框）
go build -trimpath -tags mygo_noinspector -ldflags '-H=windowsgui -s -w -X github.com/egoist/mygo.production=1' -o dist/VirDeck.exe ./cmd/app

# 编译命令行工具 CLI
go build -trimpath -ldflags '-s -w' -o dist/virdeck-cli.exe ./cmd/cli
```

> 若修改了 [`assets/product.json`](assets/product.json) 中的产品信息或图标资源，请先运行 `go generate ./assets` 更新 Windows 资源文件，再执行上述编译命令。

### 2. 首次启动与环境准备

1. 启动桌面端程序（Windows 双击 `VirDeck.exe`，同级自动创建 `VirDeckData/`；macOS 运行 `VirDeck.app`，数据默认存放在 `~/Library/Application Support/VirDeck`）。
2. 如需通过代理下载 Google 官方组件或 GitHub 上的 `scrcpy`，先点击左侧导航 **「设置 → 常规」** 填写下载代理并保存。
3. 打开 **「镜像与环境」**：
   - 在 **「运行环境」** 标签页点击 **「一键下载补齐缺失工具」**（或离线导入 `.zip` / `.tar.gz`），自动安装适配当前系统的 `emulator`、`adb` 与 `scrcpy`。
   - 在 **「系统镜像」** 标签页选择推荐镜像（x86_64 宿主默认置顶收藏 `Android 11 / 9 / 7.1 Google Play`，Apple Silicon 默认置顶收藏 `Android 11 Google Play · ARM64`）点击 **「下载安装」**（或导入本地 `.zip`）。

### 3. 创建实例与日常使用

1. 点击右上角 **「+ 新建实例」**，设置所属分组、名称前缀、数量、已安装的系统镜像、分辨率（默认 `720p`）与内存/CPU，点击 **「立即创建」**。
2. 在工作台列表中勾选实例，点击 **「启动」**（启动期间实时显示已等待秒数与引导阶段）；状态变为 `● 运行中` 后，点击 **「投屏」** 打开交互画面，也可在右侧详情栏一键切换「静音 / 有声」。
3. 打开多个投屏窗口后，可点击工作台顶部 **「投屏排版」** 一键排列窗口、开启「新投屏自动补位」或保存当前布局。

---

## 命令行工具 (CLI)

除桌面端外，可使用 `virdeck-cli`（或 `VirDeck cli ...`）执行脚本化操作：

```powershell
# 查看完整命令帮助
./dist/virdeck-cli.exe help

# 检查运行环境、宿主硬件加速与已安装镜像
./dist/virdeck-cli.exe env

# 一键下载补齐基础运行工具 (all | adb | emulator | scrcpy)
./dist/virdeck-cli.exe bootstrap -tool all

# 查看、同步或下载系统镜像
./dist/virdeck-cli.exe images -sync
./dist/virdeck-cli.exe images -download "system-images;android-30;google_apis_playstore;x86_64"

# 创建、启动、查看、投屏（支持指定初始坐标与尺寸）与停止实例
./dist/virdeck-cli.exe create -name work -count 2
./dist/virdeck-cli.exe start all
./dist/virdeck-cli.exe list
./dist/virdeck-cli.exe screen work_01
./dist/virdeck-cli.exe screen -x 40 -y 40 -width 360 -height 640 work_01
./dist/virdeck-cli.exe stop all
```

---

## 目录结构与数据迁移

### 源码目录结构

```text
VirDeck/
├── adapters/
│   ├── cli/            # 命令行交互适配层
│   └── mygonative/     # MyGo 原生 GPU 桌面界面实现
├── assets/             # 产品元数据 (product.json)、SVG/ICO 图标与 Windows 资源
├── cmd/
│   ├── app/            # 桌面端主入口（带 cli 参数时可切换命令行模式）
│   └── cli/            # 独立 CLI 可执行文件入口
├── config/             # 编译期内嵌默认配置 (defaults.json)、宿主源匹配与校验逻辑
├── core/
│   ├── adb/            # ADB 封装、引导探活、APK 安装、Shell 与安全文件收发
│   ├── catalog/        # 三级系统镜像目录（按宿主架构筛选推荐 + 官方 XML 同步 + 自定义/本地）
│   ├── downloader/     # 断点续传下载器、空闲超时监测、SHA-1 校验与 .zip/.tar.gz 防越界解压
│   ├── emu/            # 纯 Go AVD 生成、路径自愈、跨平台模拟器启停与 scrcpy 投屏控制
│   ├── instance/       # 实例注册表与 JSON 原子持久化
│   ├── screenlayout/   # Win32 / macOS 原生窗口发现、DPI 换算与多窗口固定比例平铺算法
│   └── service/        # 核心业务服务层（面向 UI/CLI 暴露统一 Service 接口）
├── docs/               # 使用指南、开发架构文档与当前 TODO
├── scripts/
│   ├── icongen/        # Windows 图标与版本资源生成器
│   └── package/        # 跨平台开发发行包构建工具 (windows/darwin/linux)
├── .github/workflows/  # 跨平台静态检查、测试与打包 CI 工作流
└── dist/               # 编译与打包输出目录（便携运行后在此生成 VirDeckData/）
```

### 运行时数据目录 (`VirDeckData/`)

便携模式下首次启动会在可执行文件旁生成 `VirDeckData/`（macOS 从 `VirDeck.app` 启动时默认位于 `~/Library/Application Support/VirDeck`）：

```text
VirDeckData/
├── config/             # 用户配置：settings.json、instances.json、images_catalog.json、groups.json、screen_layout.json
├── .android/           # 隔离的 ANDROID_USER_HOME（存放 adbkey 等）
├── avd/                # 虚拟机实例目录（.ini 与 .avd/ 独立磁盘数据）
├── sdk/                # 免安装 SDK 根目录（emulator/、platform-tools/、platforms/、system-images/）
├── scrcpy/             # scrcpy 投屏组件目录
└── downloads/          # 下载包缓存与断点续传元数据（.zip、.tar.gz、.part、.meta）
```

- **便携迁移**：关闭所有实例与主程序后，可将便携目录整体移动或复制到其他路径（Windows 下需为纯英文路径），下次启动会自动修正 AVD `.ini` 中的绝对与相对路径。
- **环境变量覆盖**：可通过 `VIRDECK_DATA_DIR` 指定数据目录位置，通过 `VIRDECK_REGISTRY` 指定实例注册表路径。
- **默认配置说明**：源码中的 [`config/defaults.json`](config/defaults.json) 仅在编译时通过 `go:embed` 内嵌为初始默认值；程序运行时只读写数据目录下 `config/` 中的用户配置，不会覆盖已有用户数据。

---

## 文档导航与开发检查

- **[使用指南 (docs/usage.md)](docs/usage.md)**：跨平台环境配置、实例管理、多窗口投屏排版、投屏与声音参数、文件收发、分组管理、空间清理与 CLI 详细用法。
- **[开发与架构文档 (docs/development.md)](docs/development.md)**：分层架构、核心技术机制、跨平台实现方案、配置规范与里程碑归档。
- **[当前开发待办 (docs/TODO.md)](docs/TODO.md)**：已完成的基础功能、人性化交互打磨与跨平台适配清单，以及下一阶段「工作会话」规划。

日常开发提交前请运行单元与静态检查：

```powershell
go test ./...
go vet ./...
```

# VirDeck 开发与架构文档

本文档面向开发者与维护者，说明 VirDeck 的模块分层、核心技术方案、跨平台实现机制、配置与构建规范，以及各阶段的演进归档。当前待办事项请以 [当前开发待办 (TODO.md)](TODO.md) 为准，用户操作说明请见 [使用指南 (usage.md)](usage.md)。

---

## 一、架构总览与模块职责

项目遵循 **「核心逻辑 (`core`) 与适配层 (`adapters`) 严格单向依赖」** 的原则：`core/` 不包含任何 GUI 依赖，桌面端 UI 与 CLI 均仅通过 [`service.Service`](../core/service/types.go) 接口与核心层交互。

```text
cmd/
 ├── app/                 # 桌面端入口（支持通过 `VirDeck cli ...` 切换 CLI 模式）
 └── cli/                 # 独立命令行可执行文件入口
adapters/
 ├── mygonative/          # MyGo 原生 GPU 自绘桌面界面、状态机规则、批量任务与弹窗
 └── cli/                 # 命令行参数解析与表格输出
core/
 ├── service/             # 业务编排层：实例生命周期、环境自检、镜像管理、分组、投屏与文件收发
 ├── instance/            # 实例注册表（ID/内部 AVD 名/显示名分离、状态管理、JSON 原子持久化）
 ├── emu/                 # 纯 Go AVD 生成、路径自愈、跨平台模拟器进程管理与 scrcpy 投屏控制
 ├── screenlayout/        # Win32 / macOS 原生窗口发现与控制、显示器工作区枚举、固定比例平铺 Pack 算法
 ├── adb/                 # ADB 命令封装、端口探活、APK 批量安装、Shell 执行与安全文件传输
 ├── catalog/             # 三级系统镜像目录（宿主架构推荐、Google 官方 XML 同步、自定义与本地扫描）
 └── downloader/          # 断点续传下载器（Range + If-Range、空闲超时 watchdog、SHA-1 校验、.zip/.tar.gz 防越界解压）
config/
 ├── defaults.json        # 编译期内嵌的初始默认配置（通过 go:embed 打包）
 └── defaults.go          # 默认配置解析、按宿主 OS/Arch 动态派生 (ForHost) 与参数校验
assets/                   # 产品元数据 (product.json)、图标（GUI icon.svg/icon.ico 与 CLI 低饱和度 Slate 灰 icon-cli.svg/icon-cli.ico）与 Windows 资源
scripts/
 ├── icongen/             # 纯 Go 图标 (.ico) 与 Windows 版本资源 (.syso) 生成工具（独立生成 GUI 与 CLI 图标资源并内嵌至对应二进制）
 └── package/             # 跨平台发行包构建工具（生成 Windows / macOS .app / Linux 归档包）
.github/workflows/        # 跨平台 CI 流水线（cross-platform.yml：Windows / macOS / Linux 测试与打包）
```

---

## 二、核心技术机制详解

### 1. 免 JDK 纯 Go AVD 引擎 (`core/emu`)
- **告别 `avdmanager` / `sdkmanager`**：创建实例时由纯 Go 直接生成 `<AVDHome>/<AVDName>.ini` 与 `<AVDName>.avd/config.ini`，并根据系统镜像类型自动写入 `PlayStore.enabled`、`tag.id`、`abi.type`、动态映射 `hw.cpu.arch`（`x86_64` → `x86_64`、`x86` → `x86`、`arm64-v8a` → `arm64`）、`hw.lcd.width/height/density`（支持 `720p` / `1080p`）及 `image.sysdir.1`。
- **自动补齐 `package.xml`**：官方系统镜像 `.zip` 解压后仅含 `source.properties` 与 `.img` 镜像，内置下载器在解压后自动生成符合 Android SDK Schema 的 `package.xml` 及 `platforms/android-<API>` 占位目录，确保 `emulator` 校验通过并在首次冷启动时自动初始化独立 `userdata-qemu.img` 与 `encryptionkey.img`。
- **显示名与内部名分离**：实例拥有稳定的短 ID（`inst.ID`）与固定内部目录名（`avd_<id>`），用户可见的 `inst.Name` 支持中文与随时重命名，改名无需移动磁盘目录或重建 AVD。

### 2. 数据目录隔离、路径自愈与跨平台环境自检 (`core/emu`)
- **自适应数据目录 (`DefaultDataDir`)**：
  - 便携模式下默认使用可执行文件同级的 `VirDeckData/`。
  - macOS 从 `.app` Bundle（`Contents/MacOS/VirDeck`）启动时，自动回退至 `~/Library/Application Support/VirDeck`，规避 macOS App Translocation 只读路径限制；所有平台均支持通过 `VIRDECK_DATA_DIR` 覆盖。
- **四大环境变量注入**：拉起 `emulator` 与 `adb` 时显式注入隔离环境：
  - `ANDROID_SDK_ROOT` / `ANDROID_HOME` → `<DataDir>/sdk`（若本地未安装免安装 SDK，自动回退扫描宿主 `ANDROID_SDK_ROOT`、macOS `~/Library/Android/sdk` 或 Linux `~/Android/Sdk`）
  - `ANDROID_AVD_HOME` → `<DataDir>/avd`
  - `ANDROID_USER_HOME` / `ANDROID_EMULATOR_HOME` → `<DataDir>/.android`
  - `ANDROID_ADB_SERVER_PORT=5137`（与宿主默认 `5037` 端口彻底隔离）
- **移动后路径自愈 (`HealAVDIniPaths`)**：每次启动时扫描 `<AVDHome>/*.ini`，将其中的 `path=` 与 `path.rel=` 重写为当前真实路径与当前平台分隔符（`filepath.Join("avd", name+".avd")`），实现文件夹跨盘或跨目录移动后开箱即用。
- **跨平台硬件加速与路径自检**：
  - **Windows**：检测 WHPX 虚拟化状态，并校验路径是否满足纯 ASCII 与可写要求。
  - **macOS**：通过 `sysctl -n kern.hv_support` 检测 `Hypervisor.framework` 支持状态，允许 UTF-8 数据目录路径。
  - **Linux**：检测 `/dev/kvm` 设备存在性与当前用户读写权限，允许 UTF-8 数据目录路径。

### 3. 进程生命周期与跨平台进程树清理 (`core/service` & `core/emu`)
- **动态偶数端口池**：停机时实例端口为 `0`；启动时从 `[5554..5584]` 分配空闲且未被 TCP 占用的偶数端口，停机后立即回收。
- **默认冷启动与两阶段优雅停机**：
  - 启动默认携带 `-no-snapshot`（并在 `config.ini` 设置 `fastboot.forceColdBoot=yes`），只持久化 `userdata` 数据盘，避免保存数 GB 内存快照导致的关机卡死或快照损坏。
  - 停止时第一阶段发送 `adb -s emulator-<port> emu kill` 触发安卓正常刷盘关机；若超出 `stopGraceSeconds`（默认 12 秒）仍未退出，进入第二阶段强制清理进程树：
    - **Windows (`proc_windows.go`)**：设置 `CREATE_NO_WINDOW (0x08000000)` 无控制台黑框，超时调用 `taskkill /F /T` 清理进程树。
    - **macOS / Linux (`proc_other.go`)**：启动时设置独立进程组 `SysProcAttr{Setpgid: true, Pgid: 0}`，超时校验 `syscall.Getpgid(pid) == pid` 后调用 `syscall.Kill(-pid, syscall.SIGKILL)` 安全清理整个子进程树且绝不误杀外部进程组。

### 4. 独立投屏、音频转发与跨平台多窗口排版 (`core/emu` & `core/screenlayout`)
- **窗口精确识别与防重开**：拉起 `scrcpy` 时注入实例标识并校验进程可执行文件路径；重复点击「投屏」仅唤醒已有窗口。
- **三层跨平台窗口排版架构**：
  - **Windows 原生层 (`native_windows.go`)**：通过 Win32 API 枚举顶层窗口与显示器工作区、处理 Per-Monitor DPI 缩放并移动窗口。
  - **macOS 原生层 (`native_darwin.go` & `close_darwin.go`)**：基于 `purego` 零 CGO 绑定 `CoreFoundation`、`CoreGraphics` (`CGWindowListCopyWindowInfo` / `CGGetActiveDisplayList`)、`AppKit` (`NSScreen.visibleFrame`) 与 `ApplicationServices` (`AXUIElement`)，在逻辑点（Points）坐标系下实现投屏窗口枚举、最小化恢复、激活、移动缩放与关闭，并在每次操作前重新校验窗口 `CGWindowNumber + PID + 可执行文件路径` 防止陈旧句柄误操作。
  - **通用启动坐标与便携回退层 (`projection_portable.go` & `native_other.go`)**：支持在启动或重开 `scrcpy` 时直接传入 `--window-x`、`--window-y`、`--window-width`、`--window-height`；在不支持原生窗口枚举的宿主（如 Linux）上返回 `screenlayout.ErrUnsupported`，自动回退为进程级投屏跟踪并在重开投屏时保留启动坐标。
- **投屏参数与 AAC 音频链路**：
  - 支持全局默认、三档场景预设与单实例覆盖（`MaxFPS`、`MaxSize`、`BitRateMbps`、`Audio`），通过 `--max-fps`、`--max-size`、`--video-bit-rate` 传给 `scrcpy`。
  - 模拟器固定携带 `-no-audio` 抑制宿主直接发声但保留客户机内部音频采集；默认配置 `runtime.projectionAudioCodec` 采用 `aac`（`--audio-codec=aac --require-audio`），兼容 Android 11+，切换声音或画面参数只需重开 `scrcpy` 窗口而无需重启安卓实例。

### 5. 安全解压、文件收发与非破坏式分组 (`core/downloader`、`core/adb` & `core/service`)
- **跨格式安全解压 (`ExtractArchiveAtomically` / `ExtractZipAtomically`)**：
  - 支持 `.zip` 与 `.tar.gz` / `.tgz`（将 tar 条目归一化为统一预检模型），保留 Unix 可执行权限位（`0755`）与符号链接（`os.ModeSymlink`）。
  - 写入任何文件前对完整条目图执行安全预检（`validateZipEntries` / `validateLinkTarget`）：采用 APFS/HFS+ 大小写折叠与 Unicode NFD 归一化键，拒绝路径越界、空路径段（`//`）、尾部点号/空格段、穿越符号链接写入文件或循环符号链接。
- **共享存储边界与预检**：
  - 严格限制路径在 `/sdcard` 或 `/storage/emulated/0` 内，并通过 `readlink -f` 防范软链接越界访问应用私有目录。
  - 接收文件前先通过 `find` 生成无软链接清单，在拉取前拦截非法文件名与大小写不敏感重名冲突。
- **非破坏式分组**：分组元数据独立保存在 `config/groups.json`，通过稳定实例 ID 关联；删除分组仅移除映射关系，不影响实例注册表与磁盘数据。

---

## 三、配置体系与构建规范

### 1. 编译期默认配置 vs 运行时用户配置
- **[`config/defaults.json`](../config/defaults.json)**：仅供开发阶段集中调整默认参数，编译时通过 `go:embed` 嵌入可执行文件并在启动时由 `config.ForHost(runtime.GOOS, runtime.GOARCH)` 选择当前宿主的下载源（`hostSources`）与兼容架构镜像预设（`images` / `armImages`）；程序运行时**绝不读取外部 `defaults.json`**。
- **`<DataDir>/config/*.json`**：运行时唯一读写的用户配置目录。首次启动自动初始化 `settings.json`、`instances.json`、`images_catalog.json`，后续升级或重启均保留用户已有配置、实例与收藏。

### 2. 默认收藏的增量初始化
镜像预设的 `favoriteVersion` 表示首次提供该默认收藏的版本，目录文件中的 `favoritesVersion` 记录已应用版本：
- 版本 `1`：初始 `Android 11 (API 30) x86_64` 默认收藏；
- 版本 `2`：新增 `Android 9 (API 28) x86_64` 与 `Android 7.1 (API 25) x86` 默认收藏；
- 版本 `3`：Apple Silicon `arm64` 宿主的 `Android 11 (API 30) arm64-v8a` 默认收藏。

首次运行加载当前宿主架构的全部默认收藏；已有目录只补充更高版本且 `favorite: true` 的条目，保留已有备注和收藏，不自动取消任何收藏。

### 3. 跨平台打包与编译命令
推荐使用 [`scripts/package`](../scripts/package/main.go) 构建各平台可执行二进制（自动设置 `CGO_ENABLED=0`、注入生产构建标志、按 `VirDeck-v<版本>-<系统>-<架构>` 规范直接产出可执行文件，并在 macOS 上配套生成 `.app` Bundle）：

```powershell
# 构建当前平台可执行程序（直接产出 dist/VirDeck-v<版本>-<系统>-<架构> 与 virdeck-cli-...）
go run ./scripts/package

# 交叉构建指定目标平台（支持 windows/amd64、darwin/arm64、darwin/amd64、linux/amd64）
go run ./scripts/package -os darwin -arch arm64
go run ./scripts/package -os darwin -arch amd64
go run ./scripts/package -os linux -arch amd64
```

若修改了 [`assets/product.json`](../assets/product.json) 或图标，先运行 `go generate ./assets` 更新 Windows 资源文件，再执行打包或编译：

```powershell
go generate ./assets
go build -trimpath -tags mygo_noinspector -ldflags '-H=windowsgui -s -w -X github.com/egoist/mygo.production=1' -o dist/VirDeck.exe ./cmd/app
go build -trimpath -ldflags '-s -w' -o dist/virdeck-cli.exe ./cmd/cli
```

### 4. 跨平台 CI 与 GitHub Release 纯手动发布
工作流配置在 [`.github/workflows/cross-platform.yml`](../.github/workflows/cross-platform.yml)，采用**纯手动触发（`workflow_dispatch`）模式**，日常 `git push` 不会自动运行，避免本地私有 Gitea 缺少 Mac Runner 的误报错：
- **手动触发发布 Pre-release / 正式版**：
  进入 GitHub 仓库 ➔ **Actions** ➔ **Cross-platform checks & Release** ➔ 点击 **Run workflow**，在弹窗中选择分支，可自定义标签名（如 `v0.9.0-beta`）与标题，勾选“是否发布 GitHub Release”与“是否标记为预发布 (Pre-release)”。
  全平台（Windows / Linux / macOS）在 GitHub 官方云端 Runner 编译完成后自动创建 Release 并挂载全量可执行程序。

---

## 四、测试与验证指南

### 1. 常规单元测试与跨平台静态检查
```powershell
go test ./...
go vet ./...
```
> 若在多核环境下并发跑 UI 测试遇到焦点干扰，可使用 `go test -p 1 ./...` 串行执行。

### 2. 原生窗口排版冒烟测试（可选）
`core/screenlayout` 包含针对真实原生窗口的枚举与排版冒烟测试：
```powershell
$env:VIRDECK_NATIVE_SCREENLAYOUT_TEST='1'
go test ./core/screenlayout -run TestNative -count=1
```

---

## 五、里程碑演进记录

- **M1–M3（2026-11-05）**：完成核心骨架、实例注册表、动态偶数端口池、两阶段优雅停机、批量操作（APK/Shell/Proxy/Clone）、CLI 适配器与 MyGo 初版界面。
- **M4 零依赖开箱即用与三级镜像管理（2026-11-06）**：
  - 实机验证纯 Go 生成 `.ini`、`config.ini`、`package.xml` 冷启动官方 `emulator`，移除 Java/JDK 依赖。
  - 统一 `VirDeckData/` 目录隔离、四大环境变量注入与 `.ini` 路径自愈。
  - 实现断点续传下载器（`Range` + `If-Range`、`SHA-1` 校验、原子解压）与三级镜像目录（推荐置顶 + 官方 XML 同步 + 自定义/本地 `.zip` 导入）。
- **设备工作台 UI 重构与使用逻辑优化（2026-11-06 ～ 2026-11-08）**：
  - 确立三栏「设备工作台」布局（左侧导航、中间列表、右侧详情，窄窗口自适应折叠）。
  - 增加删除前确认、混合状态选择感知、代理配置不一致提示与「待重启生效」标记、镜像星标收藏备注、以及下载包/AVD 缓存分区安全清理。
- **多窗口投屏排版（2026-11-08）**：实现 `core/screenlayout` 固定尺寸比例平铺、基准窗口对齐、布局保存/恢复与新投屏自动补位。
- **四项基础功能交付（2026-11-09）**：完成全局与单实例投屏/声音参数（AAC 音频验证）、无反馈路径超时与失败原因补齐、基于共享存储的安全文件收发、以及基础实例分组。
- **基础功能人性化打磨与详情栏轻量化重构（2026-11-10）**：
  - 完成文件收发远程目录直览、发送时间戳子目录与相册自动刷新、导出目录一键打开。
  - 完成投屏三档场景预设、详情栏单行投屏摘要与顶部「静音 / 有声」一键切换、详情栏头部轻量分组胶囊菜单、启动中秒数动态计时、异常行内联错误摘要及日志/错误一键复制。
- **跨平台适配与多平台打包流水线（2026-11-10）**：
  - 完成 `windows/amd64`、`darwin/arm64`（Apple Silicon）、`darwin/amd64`（Intel Mac）与 `linux/amd64` 的下载源/镜像架构动态匹配、`.tar.gz` 与符号链接安全解压、ARM64 AVD 生成、Unix 进程组清理、macOS 原生窗口排版（`purego` CoreGraphics + Accessibility API）与便携启动坐标回退、macOS `.app` 数据目录适配，以及 [`scripts/package`](../scripts/package/main.go) 和 GitHub Actions 跨平台 CI。

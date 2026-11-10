# VirDeck 开发与架构文档

本文档面向开发者与维护者，说明 VirDeck 的模块分层、核心技术方案、配置与构建规范，以及各阶段的演进归档。当前待办事项请以 [当前开发待办 (TODO.md)](TODO.md) 为准，用户操作说明请见 [使用指南 (usage.md)](usage.md)。

---

## 一、架构总览与模块职责

项目遵循 **「核心逻辑 (`core`) 与适配层 (`adapters`) 严格单向依赖」** 的原则：`core/` 不包含任何 GUI 依赖，桌面端 UI 与 CLI 均仅通过 [`service.Service`](../core/service/types.go) 接口与核心层交互。

```text
cmd/
 ├── app/                 # 桌面端入口（支持通过 `VirDeck.exe cli ...` 切换 CLI 模式）
 └── cli/                 # 独立命令行可执行文件入口
adapters/
 ├── mygonative/          # MyGo 原生 GPU 自绘桌面界面、状态机规则、批量任务与弹窗
 └── cli/                 # 命令行参数解析与表格输出
core/
 ├── service/             # 业务编排层：实例生命周期、环境自检、镜像管理、分组、投屏与文件收发
 ├── instance/            # 实例注册表（ID/内部 AVD 名/显示名分离、状态管理、JSON 原子持久化）
 ├── emu/                 # 纯 Go AVD 生成、路径自愈、emulator 进程管理与 scrcpy 投屏控制
 ├── screenlayout/        # Win32 窗口句柄发现、显示器 DPI/工作区枚举、固定比例平铺 Pack 算法
 ├── adb/                 # ADB 命令封装、端口探活、APK 批量安装、Shell 执行与安全文件传输
 ├── catalog/             # 三级系统镜像目录（内置推荐、Google 官方 XML 同步、自定义与本地扫描）
 └── downloader/          # 断点续传下载器（Range + If-Range、空闲超时 watchdog、SHA-1 校验、原子解压）
config/
 ├── defaults.json        # 编译期内嵌的初始默认配置（通过 go:embed 打包）
 └── defaults.go          # 默认配置解析、参数范围校验与旧配置归一化
assets/                   # 产品元数据 (product.json)、图标与 go generate 生成的 Windows 资源
scripts/icongen/          # 纯 Go 图标 (.ico) 与 Windows 版本资源 (.syso) 生成工具
```

---

## 二、核心技术机制详解

### 1. 免 JDK 纯 Go AVD 引擎 (`core/emu`)
- **告别 `avdmanager` / `sdkmanager`**：创建实例时由纯 Go 直接生成 `VirDeckData/avd/<AVDName>.ini` 与 `<AVDName>.avd/config.ini`，并根据系统镜像类型自动写入 `PlayStore.enabled`、`tag.id`、`abi.type=x86_64`、`hw.lcd.width/height/density`（支持 `720p` / `1080p`）及 `image.sysdir.1`。
- **自动补齐 `package.xml`**：官方系统镜像 `.zip` 解压后仅含 `source.properties` 与 `.img` 镜像，内置下载器在解压后自动生成符合 Android SDK Schema 的 `package.xml` 及 `platforms/android-<API>` 占位目录，确保 `emulator.exe` 校验通过并在首次冷启动时自动初始化独立 `userdata-qemu.img` 与 `encryptionkey.img`。
- **显示名与内部名分离**：实例拥有稳定的短 ID（`inst.ID`）与固定内部目录名（`avd_<id>`），用户可见的 `inst.Name` 支持中文与随时重命名，改名无需移动磁盘目录或重建 AVD。

### 2. 单目录隔离与路径自愈 (`VirDeckData/`)
- **四大环境变量注入**：拉起 `emulator.exe` 与 `adb.exe` 时显式注入隔离环境：
  - `ANDROID_SDK_ROOT` / `ANDROID_HOME` → `<VirDeckData>\sdk`
  - `ANDROID_AVD_HOME` → `<VirDeckData>\avd`
  - `ANDROID_USER_HOME` / `ANDROID_EMULATOR_HOME` → `<VirDeckData>\.android`（防止密钥写回系统盘 `%USERPROFILE%\.android`）
  - `ANDROID_ADB_SERVER_PORT=5137`（与宿主已安装的 Android Studio 或第三方手机助手的 `5037` 端口彻底隔离）
- **移动后路径自愈**：每次启动时扫描 `VirDeckData/avd/*.ini`，将其中的 `path=` 重写为当前真实绝对路径，实现文件夹跨盘移动后开箱即用。
- **前置环境自检**：无需等待下载模拟器即可通过系统特性检测 WHPX 虚拟化状态，并检查数据目录是否满足纯 ASCII 字符与读写权限要求。

### 3. 进程生命周期与无黑框控制 (`core/service` & `core/emu`)
- **动态偶数端口池**：停机时实例端口为 `0`；启动时从 `[5554..5584]` 分配空闲且未被 TCP 占用的偶数端口，停机后立即回收。
- **默认冷启动与两阶段优雅停机**：
  - 启动默认携带 `-no-snapshot`（并在 `config.ini` 设置 `fastboot.forceColdBoot=yes`），只持久化 `userdata` 数据盘，避免保存数 GB 内存快照导致的关机卡死或快照损坏。
  - 停止时第一阶段发送 `adb -s emulator-<port> emu kill` 触发安卓正常刷盘关机；若超出 `stopGraceSeconds`（默认 12 秒）仍未退出，第二阶段调用 `taskkill /F /T` 强制清理进程树。
- **无控制台黑框**：所有后台子进程统一设置 Win32 `CREATE_NO_WINDOW (0x08000000)`，全程不闪 CMD 窗口。

### 4. 独立投屏、音频转发与多窗口排版 (`core/emu` & `core/screenlayout`)
- **窗口精确识别与防重开**：拉起 `scrcpy` 时注入实例标识并校验进程可执行文件路径；重复点击「投屏」仅唤醒已有窗口。
- **固定尺寸比例 Pack 排版算法**：
  - 支持「当前大小 / 小 / 中 / 大 / 以窗口为基准」五种模式，保持各窗口原始宽高比，避开任务栏与范围外已有投屏窗口，空间不足时保留原位并提示，不强行压缩窗口。
  - **异步放置与短期位置预留**：针对 Win32 窗口移动的异步特性，按 `PID/HWND` 维护短期位置预留并在到位、手动拖动或超时后释放，防止连续开启「新投屏自动补位」时多个新窗口抢占同一空位。
  - **跨缩放布局持久化**：`VirDeckData/config/screen_layout.json` 记录窗口坐标、显示器工作区与 DPI 缩放比例，恢复时自动按当前显示器缩放换算。
- **投屏参数与 AAC 音频链路**：
  - 支持全局默认与单实例覆盖（`MaxFPS`、`MaxSize`、`BitRateMbps`、`Audio`），通过 `--max-fps`、`--max-size`、`--video-bit-rate` 传给 `scrcpy`。
  - 经 Android 11 实机验证，模拟器携带 `-no-audio` 可抑制宿主直接发声但**不阻断客户机内部音频采集**；同时 Android 11 默认 Opus 编码器存在采集失败问题，故编译期默认配置 `runtime.projectionAudioCodec` 采用 `aac`（`--audio-codec=aac --require-audio`），实现按需静音/播放且切换参数只需重开 `scrcpy` 窗口而无需重启安卓实例。

### 5. 安全文件收发与基础分组 (`core/adb` & `core/service`)
- **共享存储边界与预检**：
  - 严格限制路径在 `/sdcard` 或 `/storage/emulated/0` 内，并通过 `readlink -f` 防范软链接越界访问应用私有目录。
  - 接收文件前先通过 `find` 生成无软链接清单，在拉取前拦截 Windows 非法文件名（控制字符、`<>:"/\|?*`、末尾空格/点号、DOS 保留设备名、8.3 短名称别名）以及 Windows 大小写不敏感导致的重名覆盖。
- **非破坏式分组**：分组元数据独立保存在 `VirDeckData/config/groups.json`，通过稳定实例 ID 关联；删除分组仅移除映射关系，不影响实例注册表与磁盘数据。

### 6. 错误反馈与超时保护
- **全链路有界等待**：为 ADB 命令、环境检测、APK 安装设置明确超时上限；为下载器增加连续无数据流（Idle Watchdog）超时中断，保留断点以便重试。
- **有界进程输出与迟发退出日志**：模拟器与 `scrcpy` 标准输出/错误流采用线程安全的有界缓冲区收集；实例启动失败或 `scrcpy` 异常退出时自动提取关键错误尾部写入 `LastError` 与运行日志。

---

## 三、配置体系与构建规范

### 1. 编译期默认配置 vs 运行时用户配置
- **[`config/defaults.json`](../config/defaults.json)**：**仅供开发阶段集中调整默认参数**，编译时通过 `go:embed` 嵌入可执行文件并在启动时严格校验字段合法性；程序运行时**绝不读取外部 `defaults.json`**。
- **`VirDeckData/config/*.json`**：运行时唯一读写的用户配置目录。首次启动自动初始化 `settings.json`、`instances.json`、`images_catalog.json`，后续升级或重启均保留用户已有配置、实例与收藏。

### 默认收藏的增量初始化

镜像预设的 `favoriteVersion` 表示首次提供该默认收藏的版本，目录文件中的 `favoritesVersion` 记录已应用版本。原始 Android 11 收藏属于版本 1，本轮新增收藏属于版本 2；后续新增默认收藏使用更高版本号。

首次运行加载全部默认收藏；已有目录只补充更高版本且 `favorite: true` 的条目，保留已有备注和收藏，不自动取消任何收藏。未带版本标记的旧目录视为已应用版本 1，因此不会恢复用户取消的 Android 11 收藏。迁移完成后保存标记，之后取消收藏或清空备注不会被重启覆盖。

### 2. 编译与资源生成命令
修改 [`assets/product.json`](../assets/product.json) 或图标后，先更新资源再编译：

```powershell
# 1. 生成图标与 Windows 版本资源（仅修改产品信息或图标后需要）
go generate ./assets

# 2. 编译桌面端与 CLI（仅输出两个可执行文件到 dist/，不清理已有 dist/VirDeckData/）
go build -trimpath -tags mygo_noinspector -ldflags '-H=windowsgui -s -w -X github.com/egoist/mygo.production=1' -o dist/VirDeck.exe ./cmd/app
go build -trimpath -ldflags '-s -w' -o dist/virdeck-cli.exe ./cmd/cli
```

---

## 四、测试与验证指南

### 1. 常规单元测试与静态检查
```powershell
go test ./...
go vet ./...
```
> 若在多核环境下并发跑 UI 测试遇到焦点干扰，可使用 `go test -p 1 ./...` 串行执行。

### 2. Win32 原生窗口排版冒烟测试（可选）
`core/screenlayout` 包含针对真实 Win32 离屏窗口的创建、枚举与移动冒烟测试（仅操作测试进程自身创建的隐藏/离屏窗口）：
```powershell
$env:VIRDECK_NATIVE_SCREENLAYOUT_TEST='1'
go test ./core/screenlayout -run TestNative -count=1
```

---

## 五、里程碑演进记录

- **M1–M3（2026-11-05）**：完成核心骨架、实例注册表、动态偶数端口池、两阶段优雅停机、批量操作（APK/Shell/Proxy/Clone）、CLI 适配器与 MyGo 初版界面。
- **M4 零依赖开箱即用与三级镜像管理（2026-11-06）**：
  - 实机验证纯 Go 生成 `.ini`、`config.ini`、`package.xml` 冷启动官方 `emulator.exe`，移除 Java/JDK 依赖。
  - 统一 `VirDeckData/` 目录隔离、四大环境变量注入与 `.ini` 绝对路径自愈。
  - 实现断点续传下载器（`Range` + `If-Range`、`SHA-1` 校验、原子解压）与三级镜像目录（推荐置顶 + 官方 XML 同步 + 自定义/本地 `.zip` 导入）。
- **设备工作台 UI 重构与使用逻辑优化（2026-11-06 ～ 2026-11-08）**：
  - 确立三栏「设备工作台」布局（左侧导航、中间列表、右侧详情，窄窗口自适应折叠）。
  - 增加删除前确认、混合状态选择感知（显示可执行台数并允许仅操作可用实例）、代理配置不一致提示与「待重启生效」标记、镜像星标收藏备注、以及下载包/AVD 缓存分区安全清理。
- **多窗口投屏排版（2026-11-08）**：实现 `core/screenlayout` 固定尺寸比例平铺、基准窗口对齐、布局保存/恢复与新投屏自动补位。
- **四项基础功能交付（2026-11-09）**：完成全局与单实例投屏/声音参数（AAC 音频验证）、无反馈路径超时与失败原因补齐、基于共享存储的安全文件收发、以及基础实例分组。

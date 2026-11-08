# 开发与技术记录

## 当前结构

- `cmd/app`、`cmd/cli`：桌面端和命令行入口。
- `adapters/mygonative`：MyGo 原生自绘界面、状态规则、批量任务和弹窗。
- `adapters/cli`：命令行适配。
- `core/service`：实例生命周期、配置编辑、镜像安装和缓存清理。
- `core/instance`：实例注册表与原子持久化。
- `core/emu`：AVD 生成、启动、克隆、路径修复及独立投屏进程管理。
- `core/screenlayout`：Windows 窗口发现、显示器工作区与 DPI、窗口平铺算法。
- `core/adb`：ADB 操作和设备检测。
- `core/catalog`、`core/downloader`：镜像目录、断点续传、校验和解压。
- `config/defaults.json`：仅供编译前修改的新建参数、运行环境、下载源及默认镜像收藏，通过 `go:embed` 内嵌；运行时不读取外部文件。
- `assets`：图标与产品版本资源；`scripts/icongen` 仅用于资源生成。

## 当前实现补充

中文显示名称与固定内部 AVD 名称分离；已有实例通过详情区“更多操作 → 编辑”或窄窗口操作条的“编辑”按钮修改名称、内存、CPU、代理和运行模式。名称立即生效，其余参数在下次启动时应用。

系统镜像支持星标收藏、置顶和备注；新配置默认收藏 Android 11 Google Play。新建实例的镜像列表标注已安装项，默认数量 1、内存 2048 MB。

空间清理按下载压缩包、未完成下载及 AVD 缓存分区分类。AVD 清理会检查运行状态，ADB 检测失败时保守禁用；不清理用户磁盘或压缩实例数据。

投屏保留独立 scrcpy 窗口，通过实例标识和可执行文件路径识别。重复打开仅激活；控制器明确区分创建与采用已有窗口，自动补位仅处理新创建的窗口。固定尺寸 Pack 算法按障碍物边界寻找空位，保持原比例，空间不足时跳过并保留原位；支持基准窗口不动的排列。保存 / 恢复配置位于用户 `config/screen_layout.json`，记录实例 ID、窗口矩形、显示器工作区和缩放。默认间距、尺寸、自动补位默认值及超时位于编译期 `config/defaults.json`，使用偏好写入用户 `settings.json`。Win32 放置为异步请求；短期预留按 PID/HWND 验证并在实际位置确认、手动改变或超时后释放，避免连续补位抢占同一位置。

构建仅输出 `dist/VirDeck.exe`、`dist/virdeck-cli.exe`，不再自动复制文件、生成分发子目录或打 ZIP。运行数据保留在程序旁的 `VirDeckData/`，可通过环境变量覆盖。

## 验证

```powershell
go test ./...
go vet ./...
```

原生窗口冒烟测试可通过 `$env:VIRDECK_NATIVE_SCREENLAYOUT_TEST='1'; go test ./core/screenlayout -count=1` 启用，仅创建并操作测试进程自己的离屏窗口。

真实模拟器联调、资源占用和各机器虚拟化兼容性，需与单元测试、无头渲染检查区分记录。

## 四项基础功能（2026-11-09）

- 全局投屏参数及实例覆盖：FPS、最长边、码率、音频；默认静音，AAC 编码。Settings 返回/保存复制覆盖映射；重新投屏通过每实例互斥，只关闭已验证的 scrcpy 窗口，不重启安卓。
- 基础错误反馈：ADB/环境检测上限、下载无数据 watchdog、逐项失败和投屏迟发退出日志、配置载入警告和 GUI 初始化错误。未增加诊断中心、翻译库或日志落盘体系。
- 文件收发：ADB push/pull 与系统对话框；独立传输目录、防静默覆盖、接收清单校验、取消和有界输出。真实 Android 11 验证中文/空格/特殊字符传输、内容一致及非法 Windows 名称拒绝。
- 分组：groups.json 保存分组与稳定实例 ID 关系；筛选后复用现有批量操作，删除分组不删除实例。

隔离 Android 11 音频测试中默认 Opus 编码失败；AAC 播放/录制成功，故默认编译配置 runtime.projectionAudioCodec 使用 aac。既有 emulator -no-audio 不阻断 guest 音频采集，可保持宿主静音并经 scrcpy 按需播放。

## 当前开发待办

下一阶段基础功能与日后工作会话扩展见 [当前 TODO](TODO.md)。四项基础功能已实现，未勾选项均未交付，历史章节不自动构成当前实施范围。

## 历史记录说明

以下合并保留原 TODO 和 2026-11-05 规划，供追溯已确认的决策。历史章节中的版本号、阶段状态、打包方式和默认值不代表当前实现；当前构建以根目录 README 为准，使用方法以 [使用指南](usage.md) 为准。

## 原 TODO 与完成记录

## TODO

### 使用逻辑优化（2026-11-06 已认可，已实现）

- [x] 删除前增加确认：列出实例名称和数量，说明将删除应用、登录信息和本地数据；删除失败的实例保留选中，便于重试。
- [x] 按实例状态控制操作可用性：启动、停止、克隆、投屏、安装 APK、批量命令分别校验适用状态；混合选择时显示可操作数量，让用户明确选择仅操作可用实例，避免一个未就绪实例阻断整批任务。
- [x] 完善批量任务反馈：展示完成数、成功数和失败数，支持查看失败原因、重试失败项；执行期间禁用同一实例的冲突操作。
- [x] 修正代理设置交互：多选时不沿用上次弹窗的残留值，配置不一致时明确提示，由用户主动输入或清除；运行中修改后标记“待重启生效”，区分保存值与当前生效值。
- [x] 完善首次启动环境引导：逐项检查运行工具、硬件加速和已安装镜像，说明缺失项、影响及处理方式；区分推荐镜像与已安装镜像。与现有 M4 环境自补齐规划衔接。

### M4：零依赖开箱即用与灵活镜像管理（2026-11-06 已完成）

- [x] **M4.0 最小实机验证**：基于真实官方 `emulator.exe` 验证纯 Go 生成 `.ini`、`config.ini`、`package.xml` 并冷启动自动初始化 `userdata-qemu.img` 与 `encryptionkey.img`，彻底移除对 Java / JDK / `avdmanager.bat` 的依赖。
- [x] **M4.1 目录隔离与路径自愈**：统一 `VirDeckData/` 便携目录结构（`config/`、`.android/`、`avd/`、`sdk/`、`scrcpy/`、`downloads/`），注入四大隔离环境变量（`ANDROID_SDK_ROOT`、`ANDROID_AVD_HOME`、`ANDROID_USER_HOME`、`ANDROID_ADB_SERVER_PORT=5137`），启动自动修复 `.ini` 中的 `path=` 绝对路径，并检测非 ASCII 字符与目录读写权限。
- [x] **M4.2 断点续传下载器与三级镜像目录**：实现 `core/downloader`（官方 XML 动态解析、HTTP `Range` + `If-Range` `.part`/`.meta` 断点续传、强制 `SHA-1` 校验、防 Zip Slip 原子解压）与 `core/catalog`（内置 3 款推荐镜像、官方全量 XML 同步、自定义镜像增删改、本地 `.zip` 离线导入、使用中镜像防误删保护）。
- [x] **M4.3 GUI 集成与分发打包**：在「镜像与环境管理」中集成基础工具与系统镜像一键下载/离线 `.zip` 导入、Google SDK 许可协议弹窗、实时下载进度条、下载代理与最大并发数设置，并在新建实例弹窗中增加 `720p` / `1080p` 分辨率选择。

### UI 方向（2026-11-06 已选择 A，已实现）

- 已选定 A「设备工作台」：左侧紧凑导航、中间设备列表、右侧实例详情；冷灰与白色为主，蓝色强调选中状态和主要操作。
- 单选实例联动详情，多选显示批量操作条；底部保留任务进度入口，按需展开任务与日志。
- 小窗口优先保证列表可用，导航与详情按空间收起。
- 参考静态稿：`.superpowers/brainstorm/458-1793899346/content/layout-prototype.html?variant=A`（HTML 文件打开后切换到 A）。
- B「设备卡片」不采用；更早的顶栏加大表格页面示意也不采用。
- 本次确认布局方向；静态稿中的演示数据、备注等内容不自动作为新增功能需求。正式界面沿用 Go + MyGo，后续实现需结合原生组件能力细化。

### 实现说明（2026-11-06）

- 实现位置：`adapters/mygonative`（界面、状态规则、任务执行、环境引导与下载设置）；`core/emu`（纯 Go AVD 创建、路径自愈与 Schema 兼容 `package.xml` 生成）；`core/downloader`（断点续传与原子解压）；`core/catalog`（三级镜像管理）。
- 窗口宽度 ≥ 1000 显示导航，≥ 820 显示详情；更窄时仅保留列表，单选实例的操作显示在列表上方的操作条。

## 原始技术方案（2026-11-05 归档）

## 安卓多开管理器（VirDeck）：项目规划与技术方案文档

文档日期：2026-11-05　｜　状态：M1–M3 代码与单测完成（待真机 SDK 联调），M4 规划评审中　｜　语言/框架：Go + MyGo

> **本文目的**：供项目团队评审与交接使用。凡标注「已确认」的是项目负责人明确拍板的决策；标注「建议/待实测」的是技术方案中需在 M4 第一步小样联调验证的细节。

---

### 1. 一句话概述

在 Windows (x86_64) 上用 Go + MyGo 自研一个轻量、自包含的安卓多开桌面管理器（`VirDeck.exe`），底层基于官方 `emulator` 与 `adb`，**不依赖 Android Studio、不依赖 JDK/Java**，通过内置下载器按需下载补齐运行环境与系统镜像，所有数据统一收敛在程序旁的 `VirDeckData/` 目录中，支持批量创建、启动、隔离管理多个安卓虚拟机实例，按需安装 Chrome、Gmail、Facebook、WhatsApp、Instagram 等应用。

---

### 2. 背景与目标

**背景**：现有模拟器（Android Studio、BlueStacks、MSI App Player 以及一批中国大陆模拟器）要么体积臃肿、带广告，要么不符合「非中国软件」的要求。因此自研一个针对自身需求的最简、干净、可便携分发的桌面管理方案。

**核心目标**

- **极简与免 JDK（零 Java 依赖）**：用 Go 直接生成 AVD 配置文件与 SDK 包元数据，替代 `avdmanager.bat` / `sdkmanager.bat`，省去 200MB+ 的 JDK 依赖。
- **单目录收敛与可迁移**：所有配置、运行时组件（`emulator`、`adb`、`scrcpy`）、系统镜像与虚拟机数据统一存放在程序同级的 `VirDeckData/` 目录下，支持文件夹整体迁移。
- **缺失环境自补齐**：内置支持断点续传与 SHA-1 校验的下载器，检测到缺失组件或未下载镜像时，在 GUI 内弹窗确认 Google 许可协议后一键下载解压补齐。
- **方便批量多开与数据隔离**：一键批量创建干净实例、批量启停、批量装 APK、逐实例配置代理；每个实例拥有独立 `userdata`、独立设备标识（SSAID / GSF ID），登录状态各自保留。
- **可扩展的系统镜像列表**：内置优先推荐镜像 + 从 Google 官方 XML 动态拉取更多镜像列表 + 支持用户自定义添加/编辑镜像源。
- **界面与逻辑解耦**：核心逻辑全部收敛在 `core/`，以后更换 UI 形态无需重写底层。

**非目标（当前不做）**

- 游戏按键映射、宏、手柄适配（BlueStacks 类游戏模拟器方向，不在范围内）。
- 支持 ARM 架构 Windows（当前仅面向主流 Windows 10/11 x86_64 平台）。
- 让单个虚拟机实例比官方模拟器本身更省内存（壳层无法改变客机 OS 开销，通过降分辨率、无头运行与并发控制解决）。
- 深度设备指纹魔改、绕过平台风控对抗（暂不涉及）。

---

### 3. 已确认的需求与关键决策

| 维度 | 决策内容 | 状态 |
| --- | --- | --- |
| 运行平台 | Windows 10 / 11 (x86_64) 桌面端 | 已确认 |
| 软件约束 | 非中国软件，无广告；仅分发约 10 MB 的管理器壳程序，SDK 与镜像由用户在软件内下载（规避二次分发 Google Play 镜像的授权风险） | 已确认 |
| 目标应用 | Chrome、Gmail、Facebook、WhatsApp、Instagram（工具支持批量安装 APK，不要求母版预装） | 已确认 |
| 开发语言 & UI | Go + MyGo (`github.com/egoist/mygo` 原生 GPU UI) | 已确认 |
| 架构原则 | `core` 与 `adapters` 严格单向依赖，UI 仅通过 `service.Service` 接口交互 | 已确认 |
| AVD 创建方式 | **纯 Go 原生生成 `.ini`、`config.ini` 与 `package.xml`**（替代 `avdmanager.bat`，去掉 JDK 依赖；M4 第一步先做实机小样验证） | 已确认 |
| 目录结构 | 主程序旁统一 `VirDeckData/` 文件夹，集中存放 SDK、镜像、AVD 实例、scrcpy 与配置 | 已确认 |
| 环境下载补齐 | 软件内置断点续传下载器（封装于 `core/downloader`），解析官方 XML 获取直链与 SHA-1 校验值 | 已确认 |
| 系统镜像选择 | 优先推荐列表 + 官方 XML 动态解析列表 + 支持用户自定义新增/编辑镜像项 | 已确认 |
| 多开主流程 | 默认新建干净实例 + 并发 `adb install`（克隆降级为可选功能，规避 GSF ID / SSAID 冲突） | 已确认 |
| 代理方案 | 区分「下载器代理（用于拉取 Google SDK/镜像）」与「实例运行代理（模拟器 `-http-proxy` + `-dns-server`）」 | 已确认 |
| 目标机器与并发策略 | 目标机器通常为 32 GB / 64 GB 内存（常开 5 个以上）；不写死硬性并发拦截，由用户自主控制开启数量 | 已确认 |
| 启动与快照策略 | **默认采用冷启动（`-no-snapshot`）**：不保存内存休眠快照（应用与登录态仍在 `userdata` 正常持久化），关机秒退、省磁盘且彻底规避写快照损坏问题 | 已确认 |
| APK 来源与安装 | 用户自备本地 `.apk` 文件通过界面批量选择安装，软件不内置 APK 下载列表 | 已确认 |

---

### 4. 核心技术方案设计

#### 4.1 统一目录结构（`VirDeckData` 单目录收敛）

所有运行依赖、镜像与用户数据全部收敛在 `VirDeck.exe` 同级的 `VirDeckData/` 目录中：

```text
VirDeck/
├── VirDeck.exe                        # 桌面端主程序（编译启用 -H=windowsgui，无控制台黑框）
├── virdeck-cli.exe                    # 命令行工具（供脚本或自动化调用）
└── VirDeckData/                       # 统一数据与运行环境目录
    ├── config/
    │   ├── settings.json             # 全局设置（并发上限、下载器代理、ADB Server 端口等）
    │   ├── instances.json            # 实例注册表（原子写临时文件再重命名，防崩溃损坏）
    │   └── images_catalog.json       # 系统镜像目录配置（推荐项 + 缓存的官方列表 + 用户自定义项）
    ├── .android/                     # 作为 ANDROID_USER_HOME（存放 adbkey、console token 等）
    ├── avd/                          # 虚拟机实例目录 (作为 ANDROID_AVD_HOME)
    │   ├── work_01.ini               # 实例引导描述文件（每次启动自动按当前绝对路径修正 path=）
    │   └── work_01.avd/              # 实例独立磁盘数据 (config.ini, userdata-qemu.img 等)
    ├── sdk/                          # 精简 SDK 根目录 (作为 ANDROID_SDK_ROOT)
    │   ├── emulator/                 # 官方 Android Emulator 引擎 (emulator.exe, qemu 等)
    │   ├── platform-tools/           # 官方 ADB 工具 (adb.exe)
    │   ├── platforms/                # 供 emulator 校验 SDK 根目录合法性的占位目录
    │   └── system-images/            # 已下载的系统镜像（只读共享，内含自动生成的 package.xml）
    │       └── android-34/
    │           └── google_apis_playstore/
    │               └── x86_64/       # 含 system.img, vendor.img, userdata.img, source.properties, package.xml
    ├── scrcpy/                       # 投屏工具目录 (scrcpy.exe 及依赖 DLL)
    └── downloads/                    # 内置下载器的临时缓存目录（存 .part 与 .meta 续传元数据）
```

##### 目录隔离与可迁移保障（4 项关键措施）
1. **完整的进程环境变量注入**：拉起 `emulator.exe` 和 `adb.exe` 时，显式注入：
   - `ANDROID_SDK_ROOT=<VirDeckData>\sdk`、`ANDROID_HOME=<VirDeckData>\sdk`
   - `ANDROID_AVD_HOME=<VirDeckData>\avd`
   - `ANDROID_USER_HOME=<VirDeckData>\.android`（以及旧版 `ANDROID_EMULATOR_HOME`，防止密钥和配置写回系统盘 `%USERPROFILE%\.android`）
   - `ANDROID_ADB_SERVER_PORT=5137`（使用独立 ADB 服务端口，防止与用户电脑上已安装的 Android Studio 或第三方手机助手自带的 `adb` 发生版本冲突互杀）。
2. **移动文件夹后的路径自愈**：由于 `<Name>.ini` 中的 `path=` 要求绝对路径，程序每次启动时自动扫描 `VirDeckData/avd/*.ini` 并将其中的 `path=` 重写为当前所在位置的真实绝对路径，确保整个文件夹移动到任意磁盘后直接可用。
3. **路径合法性与写权限自检**：
   - Android Emulator / QEMU 对含中文（非 ASCII）路径存在已知兼容问题：程序启动时检测当前路径，若包含非 ASCII 字符则弹窗提醒用户移动到纯英文路径（如 `D:\VirDeck`）。
   - 启动时检测当前目录是否可写（防止放在 `C:\Program Files` 下无写权限），不可写时明确提示用户迁移目录或以管理员权限运行。

---

#### 4.2 纯 Go 替代 `avdmanager.bat`（去 JDK 方案与验证要点）

官方 `cmdline-tools` 中的 `avdmanager.bat` 只是用来生成 `.ini` 和 `config.ini` 的 Java 脚本。直接用 Go 生成文件可彻底移除对 JDK 的依赖，但需处理好以下 3 个底层细节（**列为 M4.0 首要实测验证项**）：

1. **生成 `<Name>.ini` 与 `<Name>.avd/config.ini`**：
   - 默认采用对多开更省显存/内存的分辨率配置：**`720x1280`、`320 dpi`**（支持用户在创建实例时选择 `720p` 或 `1080p`）。
   - 自动根据镜像类型设置 `PlayStore.enabled=true/false`、`tag.id`、`abi.type=x86_64` 及 `image.sysdir.1=system-images\android-34\google_apis_playstore\x86_64\`。
2. **解压镜像时自动补齐 `package.xml`**：
   - 官方镜像 `.zip` 解压后只有 `source.properties` 和 `.img` 文件，而 `sdkmanager` 安装时会在镜像目录额外生成一个 `package.xml`（供 `emulator.exe` 识别镜像的 API Level 和 Revision）。
   - 我们的内置下载器在解压系统镜像后，会自动读取 `source.properties` 并在该镜像目录生成符合 Android SDK Schema 的 `package.xml` 以及对应的 `platforms/android-<API>` 占位目录。
3. **M4.0 小样实测清单（写界面前先验证）**：
   - 验证首次冷启动时 `emulator.exe` 是否顺利从镜像目录自动初始化 `userdata-qemu.img` 与 `encryptionkey.img`；
   - 验证 `-no-window` 模式下 `-gpu auto` 与 `-gpu swiftshader_indirect` 的兼容性及 Google Play 登录正常。

---

#### 4.3 运行环境识别与内置下载器（`core/downloader`）

##### 1. 环境识别顺序与标准
| 检查项 | 识别方式 | 判定「已就绪」标准 | 缺失时处理 |
| --- | --- | --- | --- |
| **1. 系统 WHPX 加速** | **无需等下载 emulator**：先通过 Windows 系统特性/注册表检测 Hyper-V / WHPX 是否开启；已有 emulator 时再跑 `emulator -accel-check` | WHPX 已启用且可用 | 立即弹窗提示开启 WHPX 的管理员命令，避免下完几个 G 才发现没开虚拟化 |
| **2. ADB (`platform-tools`)** | 检查 `VirDeckData/sdk/platform-tools/adb.exe` | 文件存在且可执行 | 从官方源拉取 `platform-tools` Windows 包并解压 |
| **3. 模拟器引擎 (`emulator`)** | 检查 `VirDeckData/sdk/emulator/emulator.exe` | 文件存在且版本满足镜像最低要求 | 从官方 `repository2-3.xml` 解析最新 Windows x64 包下载解压 |
| **4. 投屏组件 (`scrcpy`)** | 检查 `VirDeckData/scrcpy/scrcpy.exe` | 文件存在 | 拉取 `scrcpy-win64` 稳定版压缩包并解压 |
| **5. 系统镜像 (`system-images`)** | 扫描 `VirDeckData/sdk/system-images/<api>/<tag>/<abi>/` | 目录内同时存在 `system.img`、`source.properties`、`package.xml` | 在镜像列表中标为「未安装」，支持一键下载补齐 |

##### 2. 内置下载器（类 `grab` 封装）技术要点
- **动态解析官方仓库 XML**：
  - 不写死带构建号的文件名，而是请求 Google 官方 `repository2-3.xml` 和各分支 `sys-img2-3.xml`，动态提取 Windows x64 下的最新 `.zip` 文件名、文件大小与 **官方 `SHA-1` 校验值**（若网络不通或解析失败，回退至内置的已知稳定版兜底直链）。
- **安全的断点续传**：
  - 下载临时文件存为 `VirDeckData/downloads/<filename>.part`，同时在 `.meta` 中记录服务器返回的 `ETag` / `Last-Modified` 与总大小；
  - 续传时携带 `Range: bytes=<offset>-` 与 `If-Range` 头，若远端文件已更新则自动从头重下，防止新旧字节拼接损坏。
- **强制 SHA-1 校验与防穿越解压**：
  - 下载完成后**强制比对 SHA-1**，校验通过后才解压；
  - 解压时剥离或映射官方 zip 的顶层目录（如 `x86_64/`），严格校验相对路径防止 Zip Slip 目录穿越，先解压到同级 `.extract_tmp` 成功后原子重命名到目标路径。
- **许可协议确认与下载代理**：
  - 首次下载 Google SDK 组件或镜像前，界面弹出 Google Android SDK License 确认框；
  - 支持在「环境与下载设置」中单独配置**下载器 HTTP/SOCKS5 代理**（与模拟器的实例代理互不干扰）。

---

#### 4.4 系统镜像列表（Catalog）设计

镜像列表由三部分组成，统一展示在「镜像管理」与「新建实例」下拉框中（同时提供**删除保护**：若有实例正在使用某镜像，禁止删除该镜像文件）：

1. **置顶优先推荐组（固定 3 项，开箱即选）**：
   - `★ Android 14 (API 34) - Google Play 商店版` (`system-images;android-34;google_apis_playstore;x86_64`) —— **Gmail / WhatsApp / FB / IG / Chrome 首选**
   - `★ Android 13 (API 33) - Google Play 商店版` (`system-images;android-33;google_apis_playstore;x86_64`) —— **高兼容次选，内存略省**
   - `★ Android 14 (API 34) - AOSP 纯净轻量版` (`system-images;android-34;default;x86_64`) —— **无 Google 服务，最省内存**
2. **官方扩展列表（从 Google 官方 `sys-img2-3.xml` 动态同步并缓存）**：
   - 自动列出官方仓库中所有提供 `x86_64` 架构的 Android 版本（如 API 31～35 的 `google_apis_playstore`、`google_apis`、`default`），避免人工手写老版本包名出错。
3. **用户自定义添加 / 本地导入**：
   - 支持用户在界面上手动新增/编辑自定义镜像项（填写名称、包 ID、自定义下载 URL 和可选 SHA-1）；
   - 支持「从本地导入 `.zip` 镜像包」，方便内网离线环境直接导入已下载好的镜像压缩包。

---

### 5. 架构与模块分层

#### 5.1 目录结构

```text
core/
  service/      Service 接口、Instance、CreateSpec、ImageItem、DownloadProgress、Event（零 GUI 依赖）
  instance/     实例注册表（名字、动态端口、代理、配置、状态），JSON 原子持久化
  catalog/      系统镜像目录管理（推荐组 + 官方 XML 解析同步 + 自定义增删改 + 本地安装扫描）
  downloader/   内置断点续传下载器（Range + If-Range、SHA-1 校验、下载代理、防穿越原子解压）
  emu/          纯 Go AVD 配置与 package.xml 生成、路径自愈、emulator 进程启停、scrcpy 拉起、WHPX 检测
  adb/          封装 adb 命令（独立端口 5137、引导探活、批量安装 APK、批量 Shell、emu kill 优雅停机）
adapters/
  mygonative/   MyGo 原生 GPU UI 桌面端实现
  cli/          命令行适配器，用于独立验证 core 或脚本化运行
cmd/
  app/          GUI 主程序入口（支持带参数切换 CLI）
  cli/          纯 CLI 独立入口
```

#### 5.2 运行时稳定性与防坑要点

- **动态偶数端口池**：停机时 `Port` 为 `0`，启动时从 `[5554..5584]` 分配空闲偶数端口，停机回收，支持管理数十个实例按需启动。
- **默认冷启动（`-no-snapshot`）+ 优雅停机**：
  - 启动时默认携带 `-no-snapshot`（并在 `config.ini` 设 `fastboot.forceColdBoot=yes`），只持久化 `userdata` 数据盘（应用数据与账号登录状态正常保留），不保存数 GB 的内存休眠快照；
  - 点击「停止」时先发 `adb -s emulator-<port> emu kill` 让系统正常 flush 磁盘并秒级退出，若 15 秒未退出再用 `taskkill /F /T` 兜底强杀进程树，彻底规避写快照超时损坏问题。
- **无黑框子进程调用**：所有后台控制台调用统一设置 Win32 `CREATE_NO_WINDOW (0x08000000)`，双击与操作全程不闪 CMD 黑框。

---

### 6. 风险与应对

| 风险项 | 说明 | 应对措施 |
| --- | --- | --- |
| **纯 Go 生成 `config.ini` 的版本兼容性** | 不同版本 `emulator.exe` 对 `config.ini` 字段或 `package.xml` 可能有隐含校验 | **在 M4.0 阶段优先做真机小样实测**，对照官方生成的文件补齐必要字段后再写 UI |
| **内存与并发瓶颈** | 每个实例约占 2.5～3.5 GB 内存，线性叠加 | 默认降分辨率至 `720x1280`、无头运行（`-no-window`）、信号量限制最大并发运行数 |
| **MyGo 框架尚处于 0.x 阶段** | 当前使用 `v0.7.3`，依赖 Go 1.27+，后续版本 API 可能变动 | `core/` 与 `adapters/mygonative` 严格隔离，锁死 `go.mod` 版本 |
| **Google 官方源网络可达性** | 国内网络直连 `dl.google.com` 或 GitHub Releases 可能较慢或不通 | 内置下载器支持配置 HTTP/SOCKS5 下载代理，并提供「导入本地 `.zip` 离线包」入口 |
| **WHPX 与第三方虚拟化冲突** | 机器若安装了旧版 VirtualBox 或关闭了 Hyper-V 会导致模拟器无法启动 | 启动时前置检测 Windows WHPX 状态，异常时给出明确修复指引 |

---

### 7. 研发里程碑与当前进度

- [x] **M1 核心骨架 + CLI（代码与 Mock 单测完成，待真机联调）**：实例注册表、动态端口池、进程生命周期管理、两阶段停机、CLI 全指令。
- [x] **M2 批量操作与克隆（代码与 Mock 单测完成，待真机联调）**：并发上限控制、批量装 APK、批量 Shell、逐实例代理设置、无黑框子进程封装。
- [x] **M3 MyGo 原生 UI 初版（代码与无头渲染单测完成）**：实例多选表格、顶部批量操作栏、实时日志面板、新建/克隆/代理/Shell 弹窗。
- [ ] **M4 去 JDK + 统一 `VirDeckData` + 内置下载器（当前阶段）**：
  - **M4.0 真机小样验证（首要）**：在真实 Windows 环境下用纯 Go 生成 `.ini` + `config.ini` + `package.xml`，实测拉起官方 `emulator.exe` 并进入系统。
  - **M4.1 目录收敛与隔离**：统一迁移至 `VirDeckData/`，注入 4 个环境变量与独立 ADB 端口 `5137`，实现启动时 `.ini` 绝对路径自动重写与中文路径检测。
  - **M4.2 下载器与镜像目录**：实现 `core/downloader`（官方 XML 解析、断点续传、SHA-1 校验、原子解压、本地离线包导入）与 `core/catalog`。
  - **M4.3 界面集成**：在 GUI 中实现「环境与镜像下载管理」弹窗（许可协议确认、实时进度条、网速显示、自定义镜像编辑）。
- [ ] **M5 真机全流程联调与分发打磨**：实测多开资源占用数据并回填文档，完成最终发布包验证。

---

### 8. 待确认事项（均已确认完毕）

- ~~目标机器配置与并发数~~：**已确认**（主力机器为 32 GB / 64 GB 内存，常开 5 个以上，由用户自主控制开启数量，不做硬性拦截）。
- ~~快照策略偏好~~：**已确认**（默认采用冷启动 `-no-snapshot`，不保存内存快照，保证关机秒退与磁盘稳定，应用与登录态由 `userdata` 正常保留）。
- ~~APK 安装包管理~~：**已确认**（用户自备本地 APK 文件批量安装，不内置 APK 下载列表）。

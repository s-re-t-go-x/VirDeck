# VirDeck 应用安全审核与加固报告

**更新日期**：2026-11-27（对应版本 `v0.10.0`）  
**审核范围**：全部非测试 Go 源码（`cmd/`、`core/`、`adapters/`、`config/`、`assets/`、`scripts/`）、预设与产品清单（[`config/defaults.json`](../config/defaults.json)、[`assets/product.json`](../assets/product.json)）及 CI/CD 工作流（[`.github/workflows/cross-platform.yml`](../.github/workflows/cross-platform.yml)）  
**当前状态**：两阶段安全审核与第一档迁移修复均已落地并通过构建与测试验证；`go1.27.4` 临时编译产物的 `govulncheck` 二进制扫描未发现已知漏洞（`No vulnerabilities found.`）。

---

## 一、安全基线与依赖概况

### 1. 语言、架构与凭据基线

- **开发语言与构建方式**：Go `1.27.4`（`CGO_ENABLED=0` 纯 Go 静态编译），通过 [`go.mod`](../go.mod) 与 [`go.sum`](../go.sum) 管理依赖。
- **UI 与平台架构**：桌面 GUI 基于 `github.com/egoist/mygo v0.10.0`。[`adapters/mygonative/app.go`](../adapters/mygonative/app.go) 通过 `WindowOptions{Content: ui.View(vm.view)}` 使用纯原生 Canvas 渲染（显式禁用调试检查器与开发者工具：`DevToolsDisabled`，发布构建 [`scripts/package/main.go`](../scripts/package/main.go) 默认注入 `-tags mygo_noinspector` 与 `-X github.com/egoist/mygo.production=1`），未使用 WebView、HTML/JS 注入或 JS-Go 桥接，因此不存在 WebView XSS 或远程页面 IPC 暴露面。
- **密钥与凭据扫描**：对全仓库源码、配置、测试、脚本及 Git 历史执行模式检索与敏感文件排查，未发现硬编码 API Key、Token、私钥或被追踪的 `.env` / `adbkey` 文件；[`.gitignore`](../.gitignore) 已正确忽略 `.env`、`.env.*`（保留 `!.env.example`）、`adbkey` 与 `adbkey.pub`。运行期凭据管理风险主要集中在代理密码的脱敏、GUI/CLI 输入回显、备份包导出以及 Windows 多用户目录权限边界。

### 2. 依赖清单与漏洞核查

| 类别 | 模块路径 | 当前版本 | 安全状态 |
| :--- | :--- | :--- | :--- |
| 工具链 | Go 标准库 | `1.27.4` | **无已知漏洞**（已从 `1.27.3` 升级，消除 `GO-2026-6859` / `CVE-2026-97054` 及 11 项构建链 cgo 修复） |
| 直接依赖 | `github.com/ebitengine/purego` | `v0.11.3` | 无已知漏洞（已从 `v0.11.1` 升级） |
| 直接依赖 | `github.com/egoist/mygo` | `v0.10.0` | 无已知漏洞，活跃维护 |
| 直接依赖 | `golang.org/x/text` | `v0.44.0` | 无已知漏洞（已从 `v0.43.0` 升级） |
| 间接依赖 | `github.com/go-text/typesetting` | `v0.3.5` | 无已知漏洞（锁文件含传递版本 `typesetting-utils v0.0.0-20260419141703-4ffe8874dabc`） |
| 间接依赖 | `golang.org/x/image` | `v0.48.0` | 无已知漏洞（已从 `v0.46.0` 升级） |
| 间接依赖 | `golang.org/x/net` | `v0.63.0` | 无已知漏洞（已从 `v0.62.0` 升级） |

### 3. `v0.10.0` 既承安全基线（SEC-01 ～ SEC-11）

历史安全报告中的基础防护经本次代码逐项核验后已确认持续生效，并纳入新基线持续维护：

- **CI/CD 供应链与权限收敛（原 SEC-01 / SEC-07 / SEC-11）**：[`cross-platform.yml`](../.github/workflows/cross-platform.yml) 中 `actions/checkout`、`actions/setup-go`、`actions/upload-artifact`、`actions/download-artifact` 均已锁定完整 40 位 Git Commit SHA；顶层权限收窄至 `contents: read`，仅 `release` Job 声明 `contents: write` 且使用 `gh release create --verify-tag`（禁止覆盖已有 Release）；`workflow_dispatch` 输入通过环境变量与标签正则校验传递，杜绝命令注入。
- **既有路径/输入/文件权限基线（原 SEC-02 ～ SEC-06 / SEC-08 / SEC-09 / SEC-10）**：镜像包 ID 与 AVD 名称路径段安全校验、XML/INI 转义与换行清洗、`io.LimitReader(..., 64<<20)` 索引大小限制、`.android`（`0o700`）与配置 JSON（`0o600`）私有权限落盘、`.gitignore` 屏蔽敏感私钥文件、内嵌跨平台 `scrcpy` SHA-256 校验以及对无校验和镜像展示「⚠ 未提供安全校验和」预警。

---

## 二、审核问题汇总表

| 编号 | 类别 | 严重级别 | 涉及位置 | 当前状态 |
| :--- | :---: | :---: | :--- | :---: |
| **F-01** | 代码 | **High** | [`core/downloader/downloader.go`](../core/downloader/downloader.go) | **已修复** |
| **F-02** | 代码 | **High** | [`core/downloader/downloader.go`](../core/downloader/downloader.go), [`core/catalog/catalog.go`](../core/catalog/catalog.go) | **已修复** |
| **F-03** | 代码 | **High** | [`core/emu/disk.go`](../core/emu/disk.go), [`core/emu/emu.go`](../core/emu/emu.go) | **已修复** |
| **F-04** | 代码 | **High** | [`core/adb/adb.go`](../core/adb/adb.go), [`core/apk/apk.go`](../core/apk/apk.go) | **已修复** |
| **F-05** | 代码 | **Medium** | [`core/apk/apk.go`](../core/apk/apk.go) | **已修复** |
| **F-06** | 密钥 | **Medium** | [`config/redact.go`](../config/redact.go), [`core/downloader/downloader.go`](../core/downloader/downloader.go), [`core/service/bootstrap.go`](../core/service/bootstrap.go), [`core/service/manager.go`](../core/service/manager.go), `adapters/` | **已修复** |
| **F-07** | 代码 | **Medium** | [`core/downloader/downloader.go`](../core/downloader/downloader.go) | **已修复** |
| **F-08** | 代码 | **Medium** | [`core/downloader/downloader.go`](../core/downloader/downloader.go), [`core/catalog/catalog.go`](../core/catalog/catalog.go), [`core/instance/registry.go`](../core/instance/registry.go), [`core/service/bootstrap.go`](../core/service/bootstrap.go) | **已修复** |
| **F-09** | 代码 | **Medium** | [`core/service/backup_disk.go`](../core/service/backup_disk.go), [`core/emu/disk.go`](../core/emu/disk.go) | **已修复** |
| **F-10** | 代码 | **Medium** | [`core/service/manager.go`](../core/service/manager.go) | **已修复** |
| **F-11** | 代码 | **Medium** | [`core/syncinput/scrcpy_channel.go`](../core/syncinput/scrcpy_channel.go) | **已修复** |
| **F-12** | 代码 | **Medium** | [`core/emu/socks_bridge.go`](../core/emu/socks_bridge.go) | **已修复** |
| **F-13** | 代码 | **Medium** | [`core/screenlayout/native_darwin.go`](../core/screenlayout/native_darwin.go), [`core/screenlayout/close_darwin.go`](../core/screenlayout/close_darwin.go) | **已修复** |
| **F-14** | 代码 | **Medium** | [`core/syncinput/capture_windows.go`](../core/syncinput/capture_windows.go), [`core/syncinput/broadcaster.go`](../core/syncinput/broadcaster.go), [`core/service/syncinput.go`](../core/service/syncinput.go) | **已修复** |
| **F-15** | 代码 | **Medium** | [`adapters/mygonative/tools_modals.go`](../adapters/mygonative/tools_modals.go) | **已修复** |
| **F-16** | 代码 | **Low** | [`core/service/`](../core/service/manager.go), [`core/emu/`](../core/emu/emu.go), [`core/adb/`](../core/adb/adb.go) | **已修复** |
| **F-17** | 代码 | **Low** | [`adapters/mygonative/`](../adapters/mygonative/app.go), [`scripts/package/main.go`](../scripts/package/main.go), [`cross-platform.yml`](../.github/workflows/cross-platform.yml) | **已修复** |
| **D-01** | 依赖 | **Medium** | [`go.mod`](../go.mod), [`go.sum`](../go.sum) | **已修复**（升至 Go `1.27.4` 及小版本依赖） |
| **D-02** | 代码/密钥 | **High** | [`core/emu/winpath_windows.go`](../core/emu/winpath_windows.go), [`core/emu/winpath_common.go`](../core/emu/winpath_common.go) | **暂缓 / 已知限制**（面向个人 PC 场景暂不迁移） |
| **D-03** | 密钥 | **Medium** | [`core/service/backup_disk.go`](../core/service/backup_disk.go), [`core/service/types.go`](../core/service/types.go), [`adapters/cli/cli.go`](../adapters/cli/cli.go), [`adapters/mygonative/tools_modals.go`](../adapters/mygonative/tools_modals.go) | **已修复**（默认脱敏 + GUI/CLI 显式开关） |
| **D-04** | 密钥 | **Medium** | [`adapters/mygonative/edit.go`](../adapters/mygonative/edit.go), [`adapters/cli/cli.go`](../adapters/cli/cli.go), [`core/emu/emu.go`](../core/emu/emu.go) | **部分修复**（UI 掩码与 CLI `VIRDECK_PROXY`/`-proxy-stdin` 已落地；非 SOCKS5 HTTP 代理进程参数保持现状） |
| **D-05** | 代码 | **Medium** | [`core/downloader/downloader.go`](../core/downloader/downloader.go), [`config/defaults.json`](../config/defaults.json) | **已修复（务实版）**（URL 隔离缓存与防未校验复用已落地；非收藏镜像直链已清空为动态官方同步） |
| **D-06** | 代码 | **Medium** | [`core/adb/transfer.go`](../core/adb/transfer.go) | **维持现状**（不采用 `exec-out tar`，依赖隔离导出目录与前置 `find` 校验） |
| **D-07** | 代码 | **Low** | [`scripts/icongen/main.go`](../scripts/icongen/main.go) | **已修复（极简版）**（移除仓库内 `.exe` 自动执行） |
| **S-01** | 代码 | **Info** | [`core/adb/transfer.go`](../core/adb/transfer.go), [`core/emu/screens.go`](../core/emu/screens.go) | **已修复** |

---

## 三、已修复问题及具体改动

### （一）第一阶段：安全基线直接修复（F-01 ～ F-17）

1. **下载、压缩包与目录安全（F-01, F-02, F-07, F-08）**
   - **解压炸弹与权限防护**（[`core/downloader/downloader.go`](../core/downloader/downloader.go)）：`ExtractZipAtomically` 与 `ExtractArchiveAtomically` 新增条目上限（`100,000`）、累计解压上限（`16 GiB`）及符号链接目标上限（`64 KiB`）；`.tar.gz` 转换阶段提前调用 `safeArchivePath`；`extractSingleZipEntry` 将写入权限掩码为 `mode.Perm() & 0o755`，剥离全局可写位，直接复用预检阶段读取的链接目标避免二次打开，并改用 `os.MkdirTemp` 随机临时目录。
   - **协议降级与续传防护**（[`core/downloader/downloader.go`](../core/downloader/downloader.go)、[`core/catalog/catalog.go`](../core/catalog/catalog.go)）：`httpClient` 增加 `CheckRedirect` 拦截 `https -> http` 降级重定向与非 `http/https` 协议重定向；`Download`、`SaveCustom` 与 `ParseRepositoryXMLForHost` 强制校验 `http`/`https` 协议并在 `https` 索引下拒绝降级至 `http`；HTTP `416` 仅在 `offset > 0` 时允许单次重试（消除无限递归）；`.meta` 校验 URL 一致性且仅写入脱敏 URL（`0o600`）；校验 `Content-Range` 与 `16 GiB` 下载上限（含 `int64` 溢出检查）。
   - **目录校验与原子写盘**（[`core/catalog/catalog.go`](../core/catalog/catalog.go)、[`core/instance/registry.go`](../core/instance/registry.go)、[`core/service/bootstrap.go`](../core/service/bootstrap.go)）：`SaveCustom` 校验 `android-` 前缀与合法 URL，`isSafePackageSegment` 拦截 ASCII 控制字符与尾随 `.`/空格，`saveLocked` 等持久化逻辑统一采用 `os.CreateTemp` + `Chmod(0o600)` + `Sync` + `Rename`；`Catalog.List` 在官方更新替换 URL 且无新 SHA-256 时清空旧 SHA-256，避免校验错配。

2. **代理与 URL 凭据全链路脱敏（F-06）**
   - **位置**：[`config/redact.go`](../config/redact.go)、[`core/downloader/downloader.go`](../core/downloader/downloader.go)、[`core/service/bootstrap.go`](../core/service/bootstrap.go)、[`core/service/manager.go`](../core/service/manager.go)、[`adapters/mygonative/`](../adapters/mygonative/app.go)、[`adapters/cli/cli.go`](../adapters/cli/cli.go)、[`cmd/app/main.go`](../cmd/app/main.go)、[`cmd/cli/main.go`](../cmd/cli/main.go)
   - **改动**：修复 `RedactProxyURL` 回退分支与 `scrubEmbeddedCreds` 在密码含未编码 `/`、`?`、`#` 时截断漏脱敏的问题；修正 `RedactError` 保留外层包装上下文；新增并导出 `RedactText`，覆盖日志、运行事件、`inst.LastError`、UI 吐司/剪贴板复制、网络报错以及 CLI/启动弹窗异常信息。

3. **ADB 命令注入/内存上限与 APK 解析防护（F-04, F-05）**
   - **ADB 边界加固**（[`core/adb/adb.go`](../core/adb/adb.go)、[`core/adb/transfer.go`](../core/adb/transfer.go)）：包名与组件名校验引入严格正则（`validPackageRe` / `validComponentRe`），阻断 `;`、`$(...)`、`|`、反引号等 shell 元字符，对含 `$` 的合法组件名使用 `transferQuote` 引用；`runBytes` 限制响应上限为 `32 MiB`，防范恶意 guest 输出打爆宿主机内存；`TransferReceive` 拒绝 `-` 开头本地路径并规范化为绝对路径。
   - **APK / Split Bundle 防炸弹**（[`core/apk/apk.go`](../core/apk/apk.go)）：`parseStringPool` 按偏移缓存去重并限定总解码字节 `16 MiB`，消除 AXML 字符串池别名 OOM 炸弹并修复 32 位整数溢出检查；`inspectSplitBundle` 与 `extractSplitBundle` 限定最多 `4096` 条目、最多读取 1 个 `manifest.json`、校验 `package_name` 格式，并在 `copyZipEntryToFile` 严格累计检查 `2 GiB` 解压总上限。

4. **备份还原 INI / qcow2 / config.ini / 元数据校验与防 UNC 泄露（F-03, F-09, F-15）**
   - **AVD 压缩包安全还原与磁盘压缩**（[`core/emu/disk.go`](../core/emu/disk.go)）：`RestoreAVD` 限定 `4096` 条目与 `64 GiB` 解压体积上限；拦截含 `:`（NTFS ADS）、尾随 `.`/空格及 Windows 保留设备名条目；拒绝覆盖已存在 `avdDir`；验证 `target` 合法性并以 `cleanIniLine` 写 `<name>.ini` 防注入；解析 `.qcow2` 头部拦截恶意 `backing-filename`（`compactOneQcow2` 同步校验 `-B` 参数）；检查 `config.ini` 拒绝危险 `*.path` / `image.sysdir.*` 覆写；还原目录与文件权限统一收紧为 `0o700` / `0o600`。
   - **元数据清洗与钳制**（[`core/service/backup_disk.go`](../core/service/backup_disk.go)）：还原时清洗 `InstanceName` 控制字符、钳制 `MemoryMB`（1024..16384）/ `Cores`（1..8）/ `StorageGB`（4..128），并严格校验 `SystemImage`、`Resolution`、`GPUMode`、`Note` 与 `Proxy` 格式。
   - **保存框 UNC 防护**（[`adapters/mygonative/tools_modals.go`](../adapters/mygonative/tools_modals.go)）：新增 `sanitizeBackupFileStem`，防止还原来源的特殊实例名在 Windows 文件保存对话框触发 UNC SMB 外连泄露凭据。

5. **模拟器启动校验、SOCKSBridge 治理、服务层竞态与私有权限落盘（F-08, F-10, F-12, F-16）**
   - **模拟器进程与路径安全**（[`core/emu/emu.go`](../core/emu/emu.go)、[`core/emu/avd_native.go`](../core/emu/avd_native.go)、[`core/emu/proc_windows.go`](../core/emu/proc_windows.go)、[`core/emu/winpath_windows.go`](../core/emu/winpath_windows.go)）：`StartProcess` 校验 `AVDName`、allowlist `GPUMode`、合法 `Proxy`（无前导 `-`/控制字符）与 `DNSServer` IP；`CreateAVDWithOptions` 消除对 `c.sdkRoot` 的并发写竞态；`isSafePathSegment` 拦截前导 `-` 与全控制字符；`forceKillTree` 优先定位 `%SystemRoot%\System32\taskkill.exe`；`winpath_windows.go` 校验 reparse 缓冲区长度上限；非 Windows 平台 `.android` 目录收敛为 `0o700`。
   - **本地代理桥接治理**（[`core/emu/socks_bridge.go`](../core/emu/socks_bridge.go)）：配置 `ReadHeaderTimeout`、`IdleTimeout`、`MaxHeaderBytes` 与 `256` 并发连接上限；追踪所有劫持连接并在 `Close()` 时主动清理；拦截自回环目标（`127.0.0.1` / `localhost` 等回环连接桥接自身），剔除 RFC 9110 hop-by-hop 请求/响应头，并校验 SOCKS5 认证响应版本字节 `0x01`。
   - **服务层原子化与锁管理**（[`core/service/manager.go`](../core/service/manager.go)、[`core/service/cleanup.go`](../core/service/cleanup.go)、[`core/service/groups.go`](../core/service/groups.go)、[`core/service/screens.go`](../core/service/screens.go)、[`core/service/tools.go`](../core/service/tools.go)）：遗留 `instances.json` 迁移从相对当前工作目录（CWD）改为绑定可执行文件所在目录、先校验 JSON 有效性并以 `0o600` 原子写入（F-10）；`DeleteSystemImage` 改用 `os.OpenRoot` 并加锁防并发删用；`CleanCaches` 重开目录前执行二次 `os.Lstat` 防符号链接替换；`Manager.Delete` 与 `startOne` 补全互斥占位与失败回滚；`ADBServerPort` 增加 `<= 65535` 校验；所有配置 JSON 与截图统一通过随机临时文件 + `0o600` 权限原子替换。

6. **同步输入端口收敛、macOS 辅助功能空指针防护与 UI/打包/CI 加固（F-11, F-13, F-14, F-17）**
   - **同步输入通道与捕获**（[`core/syncinput/scrcpy_channel.go`](../core/syncinput/scrcpy_channel.go)、[`core/syncinput/capture_windows.go`](../core/syncinput/capture_windows.go)、[`core/syncinput/broadcaster.go`](../core/syncinput/broadcaster.go)、[`core/service/syncinput.go`](../core/service/syncinput.go)）：`DialScrcpyControl` 校验 `ServerVersion` 正则、钳制分辨率、优先使用 `adb forward tcp:0` 且建连后立即移除转发端口，消除长期暴露的无认证本地监听；`capture_windows.go` 使用 `atomic.Pointer` 消除数据竞争、`PeekMessageW` 确保队列就绪防 `Stop()` 死锁、通过 `SetMasterScope` 收敛捕获范围至主控前台窗口；`service/syncinput.go` 串行化启停操作；`Broadcaster` 释放 `b.mu` 后执行耗时广播，避免单台从机卡死导致全局死锁。
   - **macOS 窗口操作保护**（[`core/screenlayout/native_darwin.go`](../core/screenlayout/native_darwin.go)、[`core/screenlayout/close_darwin.go`](../core/screenlayout/close_darwin.go)）：新增空安全 `cfRelease` 与零句柄/`utf8.ValidString` 校验，避免 `CFRelease(0)` 导致原生崩溃。
   - **UI / 打包 / CI 细节**（[`adapters/mygonative/`](../adapters/mygonative/app.go)、[`scripts/package/main.go`](../scripts/package/main.go)、[`cross-platform.yml`](../.github/workflows/cross-platform.yml)）：显式设置 `DevToolsDisabled`；`appendLog` 限长 `8192` 字节并清洗控制字符；`openLocalDir` 校验本地绝对目录并拒绝 UNC 与非目录路径；`refreshAppsList` 增加 `appsSeq` 防过期列表覆写；`scripts/package/main.go` 校验全部文件复制与写入错误；CI `native` Job 添加 `persist-credentials: false` 与 `--title="$TITLE"`。

### （二）第二阶段：第一档迁移落地（D-01 / D-03 / D-04 UI&CLI / D-05 务实版 / D-07 极简版 / S-01）

1. **`D-01`（工具链与依赖升级）**：
   - 升级 [`go.mod`](../go.mod) 与 [`go.sum`](../go.sum) 至 Go `1.27.4`、`purego v0.11.3`、`x/text v0.44.0`、`x/image v0.48.0`、`x/net v0.63.0`，并更新 [`README.md`](../README.md) 与 [`development.md`](../docs/development.md) 版本说明；二进制扫描消除 `GO-2026-6859`（`CVE-2026-97054`）。
2. **`D-03`（备份/还原代理凭据默认脱敏与显式开关）**：
   - 在 [`core/service/types.go`](../core/service/types.go) 新增 `BackupOptions` / `RestoreOptions`（含 `IncludeProxy`）及 `*WithOptions` 接口方法；[`core/service/backup_disk.go`](../core/service/backup_disk.go) 默认不导出、也不自动继承备份内的 `Proxy`，仅当显式开启 `IncludeProxy: true` 时处理且仍走严格格式校验。
   - 在 [`adapters/cli/cli.go`](../adapters/cli/cli.go) 为 `backup` / `restore` 增加 `-include-proxy` 开关（默认 `false`）；在 GUI（[`tools_modals.go`](../adapters/mygonative/tools_modals.go)、[`shell.go`](../adapters/mygonative/shell.go)、[`page_workbench.go`](../adapters/mygonative/page_workbench.go)）新增备份/还原确认弹窗及默认未勾选的代理开关，防范分享备份包泄密或导入第三方备份包时被劫持代理。
3. **`D-04`（仅 UI 掩码与 CLI 环境变量/stdin 部分）**：
   - 在 [`adapters/mygonative/edit.go`](../adapters/mygonative/edit.go) 新增 `proxyTextInput`，默认调用 `mygo/ui` 的 `.Password()` 掩码显示代理 URL 并提供“显示/隐藏”切换按钮（同时保留完整单行粘贴 `http://user:pass@host:port` 体验），覆盖新建实例（[`app.go`](../adapters/mygonative/app.go)）、编辑实例（[`edit.go`](../adapters/mygonative/edit.go)）、批量改代理（[`app.go`](../adapters/mygonative/app.go)）与全局下载代理设置（[`page_settings.go`](../adapters/mygonative/page_settings.go)），并在每次打开弹窗/设置页时自动重置为隐藏状态。
   - 在 [`adapters/cli/cli.go`](../adapters/cli/cli.go) 为 `create` 与 `proxy` 新增 `-proxy-stdin` 与 `VIRDECK_PROXY` 环境变量读取（`resolveCLIProxy` 校验长度不超过 `4096` 字节、拒绝换行与 `\x00`、禁止与命令行参数冲突，且显式传 `-url ""` 仍可清空代理）。
4. **`D-05`（务实版：缓存隔离、防无校验复用与镜像兜底收敛）**：
   - [`core/downloader/downloader.go`](../core/downloader/downloader.go) 新增 `cacheFileName`（以脱敏后完整 URL 的 SHA-256 前 8 位十六进制为前缀），隔离不同 URL 的同名压缩包与 `.part`/`.meta` 缓存；当未提供 `expectedChecksum` 时禁止直接复用本地旧文件，强制清除旧缓存并重新拉取。
   - [`config/defaults.json`](../config/defaults.json) 仅对收藏预设中的固定镜像保留带官方 SHA-1 的直链兜底，将其余非收藏预设（`system-images;android-33;google_apis_playstore;x86_64` 与存在失效 `x86_64-34_r08.zip` 链接的 `system-images;android-34;default;x86_64`）统一清空为 `"url": ""`，下载时强制先从官方 `sys-img2-3.xml` 获取实时有效链接与官方校验和，消除失效与无校验和镜像直链。
5. **`D-07`（极简版：禁止自动执行仓库内可执行文件）**：
   - [`scripts/icongen/main.go`](../scripts/icongen/main.go) 移除优先运行工作区内 `scripts/icongen/go-winres.exe` 的逻辑，仅使用系统 `PATH` 中的 `go-winres` 或版本锁定的 `go run github.com/tc-hib/go-winres@v0.3.3`，避免污染主 `go.mod` 依赖树。
6. **`S-01`（ADB 传输与投屏参数边界加固）**：
   - [`core/adb/transfer.go`](../core/adb/transfer.go) 中 `TransferSend` 改用 `transferWindowsKey` 对目标文件名执行大小写不敏感去重（消除 Linux/macOS 向 Android `/sdcard` 同时推送 `Photo.jpg` 与 `photo.jpg` 时的覆盖 Bug）；`TransferList` 对非空返回严格校验成对字段与尾部 `\x00` 结束符，拒绝 `.`/`..`/空名称及含 `/` 条目。
   - [`core/emu/screens.go`](../core/emu/screens.go) 将 `scrcpy` 标题传参改为 `--window-title=<title>` 单参数形式，隔离后续参数解析边界。

---

## 四、暂缓项、已知限制与后续建议

1. **`D-02`：Windows 多用户环境下 `%PUBLIC%` 路径暴露（已暂缓，已知限制）**
   - [`winpath_common.go`](../core/emu/winpath_common.go) 与 [`winpath_windows.go`](../core/emu/winpath_windows.go) 在非 ASCII 用户名且短路径不可用时仍会回退至 `%PUBLIC%\VirDeckData` 或 `%PUBLIC%\VirDeckLinks`。由于中文用户名在 `%LOCALAPPDATA%` 场景不满足模拟器纯 ASCII 要求，且本项目主要面向个人单机 PC 使用，高复杂度的每用户 `<UserSID>` + Win32 NTFS DACL 迁移已暂缓。**使用提示**：切勿在多用户共享 Windows 主机 / 公共云桌面环境下使用 `%PUBLIC%` 作为数据或联接目录。
2. **`D-04` 遗留部分：带认证的非 SOCKS5 HTTP 代理进程命令行可见**
   - 当为实例配置 `http://user:pass@host`（非 `socks5://`）时，启动模拟器仍通过 `-http-proxy` 参数将完整 URL 传递给 `emulator` 进程，同机具有进程枚举权限的用户或 EDR 启动审计日志仍可能读到命令行中的凭据。如在多用户主机或严格进程审计环境下运行带密码代理，建议改用 `socks5://`（自动走仅绑定 `127.0.0.1` 的本地无鉴权桥接端口）或按需将本地桥接扩展支持上游 HTTP 代理。
3. **`D-05` 校验策略保留项：[`config/defaults.json`](../config/defaults.json) 中的滚动工具与 XML 校验机制**
   - 预设系统镜像中的所有非空固定直链均已附带官方 SHA-1 校验和；非收藏条目已清空 `"url"` 以按需从 Google 官方 `sys-img2-3.xml` 获取最新有效地址与 SHA-1。
   - 滚动更新链接 `platform-tools-latest-{windows,darwin,linux}.zip` 刻意不绑定静态 SHA-256，以避免 Google 发版后立即破坏兜底下载；正常情况下优先从 HTTPS 的 `repository2-3.xml` 获取官方动态 SHA-1 校验和。
4. **`D-06`：`TransferReceive` 设备侧 TOCTOU（按风险权衡维持现状）**
   - `TransferReceive` 采用 `find` 校验树结构后再 `adb pull` 至 [`core/service/transfer.go`](../core/service/transfer.go) 创建的独立 `VirDeck-export-*` 临时目录。考虑到在 Android 7.1–14 全版本切换 `adb exec-out tar` 存在显著的 `toybox tar` 兼容性风险，而现有隔离导出目录已封堵覆写宿主机其他路径的风险，因此维持现状。
5. **持续安全工程建议**
   - 在 CI 中引入固定版本的 `govulncheck` 或 `osv-scanner` 检查。
   - 若外部工具升级或替换 `scrcpy`，保持在 `v3.3.3` 以上版本（当前 `defaults.json` 已指向 `v5.0.1`），持续规避历史已知缺陷（`CVE-2025-34449`）。
   - 后续如增加远程配置同步或扩展能力，继续坚持纯原生 Canvas 渲染架构，避免开放远程 WebView 与脚本绑定。

---

## 五、验证与回归测试记录

### 1. 安全回归与新增单元测试

- [`config/redact_test.go`](../config/redact_test.go)：覆盖含特殊字符代理密码的 `RedactProxyURL`、`RedactError` 与 `RedactText`。
- [`core/downloader/downloader_test.go`](../core/downloader/downloader_test.go)：覆盖 HTTPS 降级拒绝、HTTP `416` 防无限递归、SHA-256 校验、POSIX 掩码、URL 哈希缓存隔离及禁止无校验缓存复用。
- [`core/adb/adb_test.go`](../core/adb/adb_test.go) 与 [`core/adb/transfer_test.go`](../core/adb/transfer_test.go)：覆盖包名/组件名注入拦截、导出内存上限、大小写不敏感发送去重及 `TransferList` 严格 `\x00` 帧校验。
- [`core/apk/apk_test.go`](../core/apk/apk_test.go)：覆盖 AXML 字符串池别名防 OOM 炸弹与 Split Bundle 条目限制。
- [`core/emu/disk_test.go`](../core/emu/disk_test.go) 与 [`core/emu/screens_test.go`](../core/emu/screens_test.go)：覆盖 `RestoreAVD` 炸弹/ADS/保留名/qcow2/INI 防护与 `--window-title=` 传参。
- [`core/service/tools_test.go`](../core/service/tools_test.go)、[`adapters/cli/p3b_cli_test.go`](../adapters/cli/p3b_cli_test.go) 与 [`adapters/mygonative/p3b_test.go`](../adapters/mygonative/p3b_test.go)：覆盖备份/还原代理凭据默认脱敏策略、`-include-proxy`、`VIRDECK_PROXY` / `-proxy-stdin`、UI 代理输入框掩码切换以及日志清洗/目录校验（[`app_test.go`](../adapters/mygonative/app_test.go)）。

### 2. 最近一次完整验证（全部退出码 `0`）

- 静态检查与多平台构建：`go vet ./...`、`go build -o NUL ./cmd/app`、`go build -o NUL ./cmd/cli`、`GOOS=darwin GOARCH=arm64 go build ./core/screenlayout` 均通过。
- 全包单元测试：
  - `virdeck/adapters/cli`、`virdeck/adapters/mygonative`、`virdeck/config`、`virdeck/core/adb`、`virdeck/core/apk`、`virdeck/core/instance`、`virdeck/core/screenlayout`、`virdeck/core/syncinput`、`virdeck/scripts/package` 全部 `PASS`（`scripts/icongen` 无测试文件）。
  - `virdeck/core/catalog`、`virdeck/core/downloader`、`virdeck/core/emu`、`virdeck/core/service`（非沙盒环境运行回环网络与 NTFS Junction 测试）全部 `PASS`。
- 二进制漏洞扫描：基于 Go `1.27.4` 编译 `./cmd/app` 并运行 `govulncheck@v1.10.0 -mode binary`，结果为 `No vulnerabilities found.`。

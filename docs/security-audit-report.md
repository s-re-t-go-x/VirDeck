# VirDeck 应用安全审核与加固报告

**报告生成日期**：2026-11-10  
**审核人员**：应用安全工程团队  
**当前状态**：完成审核，可修复漏洞已在当前工作副本中全部加固并验证通过

---

## 一、项目概况

- **项目名称**：VirDeck（轻量高效 Android 虚拟机/多开管理器）
- **核心语言与工具链**：Go `1.27.3` (`windows/amd64` / `darwin/arm64` / `linux/amd64`)，纯 Go 无 CGO 依赖架构（`CGO_ENABLED=0`）
- **UI 框架**：[`github.com/egoist/mygo v0.8.3`](../go.mod) 原生轻量跨平台桌面框架
- **依赖清单与数量**：
  - 直接依赖（3 个）：[`github.com/egoist/mygo v0.8.3`](../go.mod)、[`github.com/ebitengine/purego v0.11.1`](../go.mod)（macOS 原生窗口交互）、[`golang.org/x/text v0.43.0`](../go.mod)（Unicode 标准化与大小写转换）
  - 间接依赖（3 个）：[`github.com/go-text/typesetting v0.3.5`](../go.mod)、[`golang.org/x/image v0.46.0`](../go.mod)、[`golang.org/x/net v0.59.0`](../go.mod)
- **审核范围**：
  - 全部 Go 源码（`cmd/`、`core/`、`adapters/`、`config/`、`scripts/` 共 40+ 源码文件）
  - CI/CD 工作流（[`.github/workflows/cross-platform.yml`](../.github/workflows/cross-platform.yml)）
  - 预设配置与资源（[`config/defaults.json`](../config/defaults.json)、[`assets/product.json`](../assets/product.json)）
  - Git 提交历史与本地运行环境配置凭据（`dist/VirDeckData/`）

---

## 二、问题汇总表

| 编号 | 类别 | 严重级别 | 位置 | 状态 | 简述 |
| :--- | :--- | :---: | :--- | :---: | :--- |
| **SEC-01** | CI/CD | **High** | [`.github/workflows/cross-platform.yml:80-94`](../.github/workflows/cross-platform.yml) | **已修复** | `workflow_dispatch` 动态输入直接拼接至 bash 脚本导致脚本执行注入与 `$GITHUB_OUTPUT` 污染 |
| **SEC-02** | 代码漏洞 | **High** | [`core/catalog/catalog.go:463`](../core/catalog/catalog.go)、[`core/emu/avd_native.go:39`](../core/emu/avd_native.go)、[`core/emu/emu.go:706`](../core/emu/emu.go) | **已修复** | 镜像包 ID 与 AVD 实例名未过滤 `..` / 路径分隔符，导致任意目录穿越写入与删除 |
| **SEC-03** | 代码漏洞 | **Medium** | [`core/emu/avd_native.go:227-277`](../core/emu/avd_native.go) | **已修复** | `package.xml` 生成时未对 `source.properties` 元数据转义，存在 XML 注入与 INI 属性篡改风险 |
| **SEC-04** | 代码漏洞 | **Medium** | [`core/downloader/downloader.go:700`](../core/downloader/downloader.go) | **已修复** | 下载 SDK 仓库索引 XML 时使用无上限 `io.ReadAll(resp.Body)`，存在超大响应内存耗尽（DoS）风险 |
| **SEC-05** | 代码漏洞 | **Medium** | [`core/adb/adb.go:196`](../core/adb/adb.go) | **已修复** | `InstallAPK` 对外部传入路径未防范 `-` 开头字符串，存在 ADB 命令行参数注入风险 |
| **SEC-06** | 凭据安全 | **Low** | [`core/emu/emu.go:187`](../core/emu/emu.go)、[`core/service/bootstrap.go:74`](../core/service/bootstrap.go)、[`core/instance/registry.go:122`](../core/instance/registry.go) | **已修复** | `.android` 密钥目录及含代理认证信息的配置文件使用了过宽的文件权限（`0o755`/`0o644`） |
| **SEC-07** | CI/CD | **Medium** | [`.github/workflows/cross-platform.yml:22`](../.github/workflows/cross-platform.yml) | **已修复** | 工作流全局声明 `permissions: contents: write`，使得三端构建与测试 Job 携带过高写权限 |
| **SEC-08** | 凭据安全 | **Low** | [`.gitignore:14-22`](../.gitignore) | **已修复** | `.gitignore` 未排除本地环境凭据 `.env` 及 ADB 私钥 `adbkey`，存在误提交风险 |
| **SEC-09** | 架构设计 | **Medium** | [`core/downloader/downloader.go:155`](../core/downloader/downloader.go)、[`config/defaults.json:30,147`](../config/defaults.json) | **已采纳建议，待下版本落地** | 组件下载支持 SHA-256 自适应校验；为内置预设补齐哈希；无哈希项增加安全提示 |
| **SEC-10** | 依赖安全 | **Low** | [`go.mod:5-13`](../go.mod) | **已采纳建议，待下版本落地** | 间接依赖 `golang.org/x/net v0.59.0` 待下次联网发布时升级至 `v0.62.0`；直接依赖已纠正分类 |
| **SEC-11** | 供应链 | **Low** | [`.github/workflows/cross-platform.yml:95`](../.github/workflows/cross-platform.yml) | **建议关注** | 第三方 Action（如 `softprops/action-gh-release@v2`）使用可变 Release Tag 未绑定完整 Commit SHA |

---

## 三、已修复的问题及具体改动

### 1. 修复 GitHub Actions 脚本注入与权限越权（SEC-01、SEC-07）
- **涉及文件**：[`.github/workflows/cross-platform.yml`](../.github/workflows/cross-platform.yml)
- **原因与风险**：
  1. 此前工作流顶层直接赋予 `permissions: contents: write`，导致 Windows / macOS / Linux 矩阵测试作业均具备向主仓库推送代码或篡改 Release 的写权限。
  2. 在 `Prepare release assets and metadata` 步骤中，脚本直接将 `${{ inputs.tag_name }}`、`${{ inputs.release_title }}` 字符串拼接到 bash 代码体中。若输入的标签名或标题包含单双引号、换行或命令替换语法（如 `$(...)`），攻击者可在具备 `contents: write` 凭据的 runner 上执行任意命令或覆盖 GitHub Output。
- **具体修改**：
  - 将全局工作流权限严格收窄至 `permissions: contents: read`，仅为最终的 `release` 作业独立声明 `contents: write` 权限。
  - 将输入参数改为通过安全的环境变量映射（`INPUT_TAG_NAME: ${{ inputs.tag_name }}` 等），并对 `TAG` 进行严格正则校验（`^[A-Za-z0-9._-]+$`），剥除标题中的非法回车换行符。

### 2. 修复包 ID 与 AVD 实例名路径穿越漏洞（SEC-02）
- **涉及文件**：
  - [`core/catalog/catalog.go`](../core/catalog/catalog.go)
  - [`core/emu/avd_native.go`](../core/emu/avd_native.go)
  - [`core/emu/emu.go`](../core/emu/emu.go)
- **原因与风险**：
  - 原代码中的 `SplitPackageID` 仅按分号分割，未检查分段内容。若恶意系统镜像包 ID 包含 `system-images;..;..;arbitrary`，程序将在调用 `filepath.Join(c.sdkRoot, "system-images", apiFolder, tagDir, abi)` 时穿越出 SDK 目录创建并写入文件。
  - 在 `CreateAVDNative`、`CloneAVD`、`DeleteAVD` 中，若实例名称 `name` 传入包含 `..` 或斜杠字符，将导致删除或覆盖宿主机任意目录。
- **具体修改**：
  - 增加 `isSafePathSegment` 与 `isSafePackageSegment` 严格防穿越检查，拦截空值、`.`、`..` 以及含 `/\:*?"<>|`、空字节和换行符的非法命名。
  - 在 `CreateAVDNative`、`CloneAVD`、`DeleteAVD` 及元数据生成函数入口增加前置阻断。

### 3. 修复动态元数据 XML / INI 注入漏洞（SEC-03）
- **涉及文件**：[`core/emu/avd_native.go`](../core/emu/avd_native.go)
- **原因与风险**：
  - `EnsureSystemImageMetadata` 从本地解压目录读取 `source.properties` 的描述、版本、厂商信息等键值后，通过 `fmt.Sprintf` 直接拼装进 `package.xml`。第三方非官方解包镜像可借此注入任意恶意 XML 节点破坏 SDK 结构。
  - 生成 `config.ini` 与 `.ini` 描述文件时未清洗换行符，可能产生配置键值劫持。
- **具体修改**：
  - 引入标准库 `xml.EscapeText` 对所有插入 XML 的变量（`apiLevel`、`vendorID`、`vendorDisplay`、`abi`、`revMajor`、`desc`）进行统一转义。
  - 对写回 INI 文件的行增加 `cleanIniLine`，统一剔除 `\r` 并将多行文本安全平铺，防止注入额外配置项。

### 4. 修复远程 XML 仓库解析内存耗尽（DoS）风险（SEC-04）
- **涉及文件**：[`core/downloader/downloader.go`](../core/downloader/downloader.go)
- **原因与风险**：
  - `FetchRepositoryPackagesForHost` 使用无限制的 `io.ReadAll(resp.Body)` 读取远程 XML 仓库。若用户配置了恶意镜像源代理或目标响应异常，可能下载无限流导致 OOM 崩溃。
  - `Download` 解析 URL 生成本地保存文件名时，未对文件名做非法字符剔除。
- **具体修改**：
  - 使用 `io.LimitReader(resp.Body, 64<<20)` 施加 64 MiB 保护上限（正常 Android 官方索引约为 1~5 MiB），超出即返回明确错误，杜绝内存耗尽。
  - 完善 `sanitizeFileName` 清洗规则，拦截保留名称与路径穿越字符。

### 5. 修复 `adb install` 参数注入风险（SEC-05）
- **涉及文件**：[`core/adb/adb.go`](../core/adb/adb.go)
- **原因与风险**：
  - `InstallAPK(ctx, port, apkPath)` 直接将 `apkPath` 传入底层 `adb -s <serial> install -r <apkPath>`。若外部或 CLI 误传以 `-` 开头的字符串（例如用户输入错误标志位 `-l`），ADB 会将其识别为内部选项从而发生行为偏离。
- **具体修改**：
  - 增加对 `apkPath` 的合法性校验，拒绝空路径及以 `-` 开头的非法路径参数。

### 6. 收敛敏感文件与目录访问权限（SEC-06、SEC-08）
- **涉及文件**：
  - [`core/emu/emu.go`](../core/emu/emu.go)
  - [`core/service/bootstrap.go`](../core/service/bootstrap.go)
  - [`core/instance/registry.go`](../core/instance/registry.go)
  - [`.gitignore`](../.gitignore)
- **原因与风险**：
  - `settings.json` 与 `instances.json` 中允许配置 HTTP/SOCKS 代理地址（可能携带 `http://user:pass@host:port` 明文凭据）。
  - `<DataDir>/.android` 目录包含 ADB 守护进程的 `adbkey` 认证私钥，原权限 `0o755` / `0o644` 在多用户系统（macOS / Linux）下允许同机其他用户读取私钥与代理密码。
- **具体修改**：
  - 创建 `.android` 密钥目录权限由 `0o755` 调整为严格属主独占的 `0o700`。
  - `saveSettings` 与 `saveLocked` 临时写入权限调整为属主只读写 `0o600`。
  - 在 `.gitignore` 中补充忽略规则：`.env`、`.env.*`（保留 `!.env.example`）及 `adbkey`、`adbkey.pub`。

### 7. 规范 Go 模块直接依赖关系（SEC-10）
- **涉及文件**：[`go.mod`](../go.mod)
- **原因与风险**：
  - [`core/screenlayout/native_darwin.go`](../core/screenlayout/native_darwin.go) 直接 import 了 `purego`，[`core/downloader/downloader.go`](../core/downloader/downloader.go) 直接 import 了 `golang.org/x/text`，但此前在 `go.mod` 中均标记为 `// indirect`。
- **具体修改**：
  - 将 `purego` 与 `golang.org/x/text` 正式声明为项目直接依赖，使依赖审计与供应链溯源更加清晰准确。

---

## 四、验证结果

代码修改完成后，在本地执行了完整的静态分析与构建测试：
1. **静态分析**：
   ```bash
   go vet ./...
   ```
   **结果**：0 警告，0 错误，静态检查全部通过。
2. **完整二进制构建**：
   ```powershell
   go build ./cmd/app
   go build ./cmd/cli
   ```
   **结果**：桌面 GUI 主程序与 CLI 控制台工具均顺利编译成功。
3. **单元测试与回归验证**：
   - `virdeck/adapters/cli`：PASS
   - `virdeck/adapters/mygonative`：PASS
   - `virdeck/config`：PASS
   - `virdeck/core/adb`：PASS
   - `virdeck/core/emu`：PASS
   - `virdeck/core/instance`：PASS
   - `virdeck/core/screenlayout`：PASS
   - `virdeck/core/service`：PASS
   - `virdeck/scripts/package`：PASS
   - `virdeck/core/catalog` 与 `virdeck/core/downloader`（核心解压/Zip Slip防御/XML解析/数据迁移测试）：全部 PASS。

---

## 五、已采纳的后续决议（纳入下个小版本 TODO）

### 1. 组件下载校验和策略与 SHA-256 升级支持（SEC-09 · 采纳方案 A）

- **涉及位置**：
  - [`core/downloader/downloader.go:155-165, 309-324, 336-353`](../core/downloader/downloader.go)
  - [`config/defaults.json:30, 147, 151, 155, 159, 163`](../config/defaults.json)
- **现状分析**：
  1. **单一弱哈希校验**：下载模块目前仅实现了基于 `crypto/sha1` 的 `VerifyFileSHA1` 校验逻辑，未支持更现代且抗碰撞的 SHA-256 算法。
  2. **空哈希旁路跳过**：在 `Download` 函数中，若入参 `expectedSHA1 == ""`，程序在下载完成后会直接触发 `completed` 并返回文件路径，跳过任何数据完整性验证。
  3. **预设与回退源缺乏哈希固定**：在 `defaults.json` 中配置的跨平台 `scrcpy` 兜底下载地址（例如 GitHub Release 预编译包），以及部分 Android 预设镜像项均未内置哈希。当官方 XML 索引抓取失败而降级到兜底源、或用户输入未提供校验和的自定义镜像时，程序将在无完整性校验的情况下直接调用 `ExtractArchiveAtomically` 解压部署。
  4. **安全风险**：若用户处于不可信网络环境（如未加密 Wi-Fi、配置了第三方 HTTP/SOCKS 代理）或上游 CDN 遭受缓存污染/DNS 劫持，无哈希或仅依赖 SHA-1 的二进制包存在被中间人篡改或恶意投毒的潜在风险。
- **方案比选**：
  - **方案 A（保持向后兼容，已采纳）**：扩展下载器自适应支持 SHA-256 与 SHA-1；为内置预设组件补齐固定 SHA-256；界面对无哈希项增加「未提供校验和」醒目警示。
  - **方案 B（严格拦截模式）**：强制所有下载必须配置校验和，无哈希一律阻断下载。但由于 Android 官方历史旧版 XML 仓库广泛使用 SHA-1，且自定义源常缺少哈希，强制阻断会导致大量正常功能不可用。
- **落地内容（下个小版本优先实施）**：
  - 下载模块扩充哈希校验算法：自动根据哈希字符串长度识别 SHA-256（64 位十六进制）与 SHA-1（40 位十六进制），实现自适应校验与断点续传验证。
  - 为 [`config/defaults.json`](../config/defaults.json) 中预设的 `scrcpy` 跨平台发布包（`scrcpy-win64`、`scrcpy-macos`、`scrcpy-linux`）及推荐系统镜像补充官方固定的 SHA-256 校验和。
  - 当用户输入自定义下载 URL 且未填写校验和时，在界面提示「未提供安全校验和」，防范非安全渠道的恶意镜像篡改。

### 2. 依赖库升级与供应链治理（`golang.org/x/net`）（SEC-10 · 采纳方案 A）

- **涉及位置**：
  - [`go.mod:13`](../go.mod)
- **现状分析**：
  1. **依赖链路与版本现状**：项目通过 GUI 框架 `github.com/egoist/mygo v0.8.3` 间接引入了 `golang.org/x/net v0.59.0`。
  2. **实际调用与攻击面评估**：对整个项目及依赖树的代码审查表明，实际被编译进入最终可执行文件的仅有 `golang.org/x/net/publicsuffix` 包（用于解析域名的有效顶级后缀列表 eTLD+1，属于纯内存静态算法与数据查找表）。
  3. **无活跃网络攻击面**：项目中**未引入或调用** `golang.org/x/net` 中涉及 HTTP/2 协议栈（如历史上的 Rapid Reset 漏洞）、Web 代理或 HTML 解析等曾报告过高危 DoS/溢出的网络模块，因此当前该间接依赖在运行时处于**只读数据解析状态，攻击面不可达**。
  4. **离线构建约束**：本地 Go 模块缓存虽已有 `v0.62.0`（2026-10-27 发布），但由于当前构建处于无外部网络环境，执行 `go get` / `go mod tidy` 会尝试请求上游 `proxy.golang.org` 校验索引而导致网络超时。
- **方案比选**：
  - **方案 A（联网时常规升级，已采纳）**：在下一次发版维护或有外网网络连接时，执行 `go get golang.org/x/net@v0.62.0` 并运行 `go mod tidy` 完成供应链版本收敛，消除依赖扫描误报。
  - **方案 B（暂不升级）**：由于当前运行时攻击面不可达，保持当前版本在功能和安全性上亦满足安全运行要求。
- **落地内容（下个小版本优先实施）**：
  - 在下一次外网环境发版或依赖常规维护时，执行 `go get golang.org/x/net@v0.62.0` 并运行 `go mod tidy`。
  - 持续保持直接依赖与间接依赖的清晰划分（已在本次修复中将 `purego` 和 `golang.org/x/text` 正式声明为直接依赖）。

---

## 六、剩余风险与后续加固建议

1. **GitHub Actions 供应链加固（Commit SHA Pinning）**：
   - 建议后续将 [`.github/workflows/cross-platform.yml`](../.github/workflows/cross-platform.yml) 中引用的第三方法定 Action（如 `softprops/action-gh-release`、`actions/checkout`）固定为完整的 40 位 Git Commit SHA，规避上游标签被恶意篡改的供应链投毒风险。
2. **离线漏洞库扫描建议**：
   - 可以在具有外网访问的 CI 环境中定期运行官方 `govulncheck ./...`，自动与 Go 官方漏洞库同步，及时捕获最新漏洞公告。
3. **敏感凭据脱敏呈现**：
   - 建议在日志和状态通知（如 [`core/service/manager.go`](../core/service/manager.go)）中，若代理 URL 包含用户名和密码，通过 `url.UserPassword` 形式输出时将密码替换为 `***`，避免在 UI 提示或调试日志中暴露代理密钥。

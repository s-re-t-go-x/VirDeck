# VirDeck

Windows x64 安卓多实例管理器，使用 Go + MyGo 原生 UI，基于 Android Emulator、ADB 和 scrcpy 管理设备。SDK 工具与镜像通过软件按需下载或离线导入，无需安装 Java / JDK。

## 编译

在项目根目录运行，Go 版本以 `go.mod` 为准：

```powershell
go build -trimpath -tags mygo_noinspector -ldflags '-H=windowsgui -s -w -X github.com/egoist/mygo.production=1' -o dist/VirDeck.exe ./cmd/app
go build -trimpath -ldflags '-s -w' -o dist/virdeck-cli.exe ./cmd/cli
```

双击 `dist/VirDeck.exe` 启动桌面端；命令行帮助使用 `dist/virdeck-cli.exe help`。运行时数据位于 `dist/VirDeckData/`，已有数据保留，不属于编译输出。

产品信息或图标修改后，先运行 `go generate ./assets` 更新 Windows 资源，再执行上面的编译命令。

## 配置

[config/defaults.json](config/defaults.json) 仅供开发时集中修改默认参数，通过 `go:embed` 编译进可执行文件。修改后需重新编译；运行时不读取项目目录或程序旁的 `defaults.json`。

用户只需拿到单个可执行文件。首次运行会自动创建程序旁的 `VirDeckData/`，并初始化 `config/settings.json`、`config/instances.json` 和 `config/images_catalog.json`。后续读取和保存这些用户配置，保留实例、设置和收藏，不会在启动时重置。

可使用 `VIRDECK_DATA_DIR` 指定数据目录，`VIRDECK_REGISTRY` 指定实例注册表路径。

## 项目目录

```text
VirDeck/
├── adapters/       # UI 与 CLI
├── assets/         # 产品元数据与图标
├── cmd/            # 两个程序入口
├── config/         # 默认配置与加载逻辑
├── core/           # 核心服务
├── docs/           # 使用指南、开发与历史记录
├── scripts/icongen # Go 图标与版本资源生成器
├── dist/           # 两个可执行文件；运行后生成 VirDeckData
├── go.mod
├── go.sum
└── README.md
```

## 文档与检查

- [使用指南](docs/usage.md)：环境准备、镜像、实例编辑、清理和日志。
- [开发与技术记录](docs/development.md)：模块职责、已完成事项及合并归档的原始方案。

```powershell
go test ./...
go vet ./...
```

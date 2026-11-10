package screenlayout

import "errors"

// ErrUnsupported means native window discovery or manipulation is unavailable.
var ErrUnsupported = errors.New("当前平台不支持原生窗口操作；Linux/Wayland 可使用投屏启动坐标，窗口位置由桌面合成器决定")

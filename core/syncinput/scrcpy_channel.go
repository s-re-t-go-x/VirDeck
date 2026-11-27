package syncinput

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ControlSink injects normalized touch, scroll, key, and text events into one Android device.
type ControlSink interface {
	InstanceID() string
	ScreenSize() (width, height int)
	SendTouch(action uint8, rx, ry float64, jitterPx int) error
	SendScroll(rx, ry float64, hScroll, vScroll float32) error
	SendKey(keycode uint32) error
	SendText(text string) error
	Close() error
}

// ScrcpyControlConfig configures a headless scrcpy-server control-only channel.
type ScrcpyControlConfig struct {
	InstanceID       string
	Port             int
	ADBBin           string
	ADBEnv           []string
	ScrcpyServerPath string
	ServerVersion    string
	DeviceWidth      int
	DeviceHeight     int
}

// ScrcpyControlClient manages one scrcpy-server process and its forwarded TCP control socket.
type ScrcpyControlClient struct {
	id        string
	port      int
	adbBin    string
	adbEnv    []string
	localPort int
	width     int
	height    int

	writeMu sync.Mutex
	conn    net.Conn
	cmd     *exec.Cmd

	clipSeq   atomic.Uint64
	closeOnce sync.Once
}

const remoteServerPath = "/data/local/tmp/virdeck-scrcpy-server.jar"

var validScrcpyVersionRe = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)+$`)

// DetectScrcpyVersion runs `scrcpy --version` and parses its version string.
func DetectScrcpyVersion(ctx context.Context, scrcpyBin string) (string, error) {
	scrcpyBin = strings.TrimSpace(scrcpyBin)
	if scrcpyBin == "" {
		return "", errors.New("未找到 scrcpy 可执行文件")
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, scrcpyBin, "--version")
	hideConsole(cmd)
	out, err := cmd.CombinedOutput()
	if ver := ParseScrcpyVersion(string(out)); ver != "" {
		return ver, nil
	}
	if err != nil {
		return "", fmt.Errorf("检测 scrcpy 版本失败: %w", err)
	}
	return "", fmt.Errorf("无法解析 scrcpy 版本输出: %q", strings.TrimSpace(string(out)))
}

// QueryDeviceSize queries Android's current display size via `adb shell wm size`.
func QueryDeviceSize(ctx context.Context, adbBin string, adbEnv []string, port int) (int, int, error) {
	if strings.TrimSpace(adbBin) == "" || port <= 0 {
		return 0, 0, errors.New("无效的 adb 路径或实例端口")
	}
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, adbBin, "-s", fmt.Sprintf("emulator-%d", port), "shell", "wm", "size")
	if len(adbEnv) > 0 {
		cmd.Env = adbEnv
	}
	hideConsole(cmd)
	out, err := cmd.CombinedOutput()
	if w, h, ok := ParseWMSize(string(out)); ok {
		return w, h, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("查询设备分辨率失败: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return 0, 0, fmt.Errorf("未能解析设备分辨率: %q", strings.TrimSpace(string(out)))
}

// DialScrcpyControl launches scrcpy-server in control-only mode and opens a forwarded TCP control socket.
func DialScrcpyControl(ctx context.Context, cfg ScrcpyControlConfig) (*ScrcpyControlClient, error) {
	if cfg.InstanceID == "" || cfg.Port <= 0 {
		return nil, errors.New("无效的实例或端口")
	}
	if strings.TrimSpace(cfg.ADBBin) == "" {
		return nil, errors.New("未找到 adb 可执行文件")
	}
	if strings.TrimSpace(cfg.ScrcpyServerPath) == "" {
		return nil, errors.New("未找到 scrcpy-server 文件")
	}
	if _, err := os.Stat(cfg.ScrcpyServerPath); err != nil {
		return nil, fmt.Errorf("无法访问 scrcpy-server: %w", err)
	}
	if strings.TrimSpace(cfg.ServerVersion) == "" || !validScrcpyVersionRe.MatchString(strings.TrimSpace(cfg.ServerVersion)) {
		return nil, fmt.Errorf("无效的 scrcpy 版本格式: %q", cfg.ServerVersion)
	}
	cfg.ServerVersion = strings.TrimSpace(cfg.ServerVersion)
	if cfg.DeviceWidth <= 0 {
		cfg.DeviceWidth = 720
	} else if cfg.DeviceWidth > 65535 {
		cfg.DeviceWidth = 65535
	}
	if cfg.DeviceHeight <= 0 {
		cfg.DeviceHeight = 1280
	} else if cfg.DeviceHeight > 65535 {
		cfg.DeviceHeight = 65535
	}

	serial := fmt.Sprintf("emulator-%d", cfg.Port)
	if out, err := runADB(ctx, cfg.ADBBin, cfg.ADBEnv, "-s", serial, "push", cfg.ScrcpyServerPath, remoteServerPath); err != nil {
		return nil, fmt.Errorf("推送 scrcpy-server 失败: %w (%s)", err, strings.TrimSpace(out))
	}

	scid := randomPositiveSCID()
	socketName := fmt.Sprintf("localabstract:scrcpy_%s", scid)
	var localPort int
	if out, err := runADB(ctx, cfg.ADBBin, cfg.ADBEnv, "-s", serial, "forward", "tcp:0", socketName); err == nil {
		if p, perr := strconv.Atoi(strings.TrimSpace(out)); perr == nil && p > 0 && p <= 65535 {
			localPort = p
		}
	}
	if localPort == 0 {
		var err error
		localPort, err = reserveLocalPort()
		if err != nil {
			return nil, fmt.Errorf("分配本地转发端口失败: %w", err)
		}
		forwardSpec := fmt.Sprintf("tcp:%d", localPort)
		if out, err := runADB(ctx, cfg.ADBBin, cfg.ADBEnv, "-s", serial, "forward", forwardSpec, socketName); err != nil {
			return nil, fmt.Errorf("建立 adb forward 失败: %w (%s)", err, strings.TrimSpace(out))
		}
	}
	forwardSpec := fmt.Sprintf("tcp:%d", localPort)

	removeForward := func() {
		fctx, fcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer fcancel()
		_, _ = runADB(fctx, cfg.ADBBin, cfg.ADBEnv, "-s", serial, "forward", "--remove", forwardSpec)
	}

	serverCmdLine := fmt.Sprintf(
		"CLASSPATH=%s app_process / com.genymobile.scrcpy.Server %s scid=%s log_level=error video=false audio=false control=true tunnel_forward=true send_device_meta=false send_frame_meta=false send_codec_meta=false send_dummy_byte=true cleanup=false",
		remoteServerPath, cfg.ServerVersion, scid,
	)
	cmd := exec.Command(cfg.ADBBin, "-s", serial, "shell", serverCmdLine)
	if len(cfg.ADBEnv) > 0 {
		cmd.Env = cfg.ADBEnv
	}
	cmd.WaitDelay = time.Second
	hideConsole(cmd)
	var serverLog limitedBuffer
	cmd.Stdout = &serverLog
	cmd.Stderr = &serverLog
	if err := cmd.Start(); err != nil {
		removeForward()
		return nil, fmt.Errorf("启动 scrcpy-server 控制服务失败: %w", err)
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- cmd.Wait()
	}()

	conn, err := dialWithRetry(ctx, fmt.Sprintf("127.0.0.1:%d", localPort), serverDone, &serverLog)
	// Always tear down the forwarded port right after establishing (or failing) the control connection.
	removeForward()
	if err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, err
	}

	// Drain any incoming clipboard/ack bytes so the server write buffer never blocks.
	go func() {
		_, _ = io.Copy(io.Discard, conn)
	}()

	return &ScrcpyControlClient{
		id:        cfg.InstanceID,
		port:      cfg.Port,
		adbBin:    cfg.ADBBin,
		adbEnv:    append([]string(nil), cfg.ADBEnv...),
		localPort: localPort,
		width:     cfg.DeviceWidth,
		height:    cfg.DeviceHeight,
		conn:      conn,
		cmd:       cmd,
	}, nil
}

func dialWithRetry(ctx context.Context, addr string, serverDone <-chan error, serverLog *limitedBuffer) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()

	for {
		c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
			var dummy [1]byte
			_, rErr := io.ReadFull(c, dummy[:])
			_ = c.SetReadDeadline(time.Time{})
			if rErr == nil {
				return c, nil
			}
			_ = c.Close()
		}
		select {
		case sErr := <-serverDone:
			msg := strings.TrimSpace(serverLog.String())
			if msg != "" {
				return nil, fmt.Errorf("scrcpy-server 提前退出 (%v): %s", sErr, msg)
			}
			return nil, fmt.Errorf("scrcpy-server 提前退出: %v", sErr)
		case <-dialCtx.Done():
			msg := strings.TrimSpace(serverLog.String())
			if msg != "" {
				return nil, fmt.Errorf("连接 scrcpy-server 控制通道超时 (%w): %s", dialCtx.Err(), msg)
			}
			return nil, fmt.Errorf("连接 scrcpy-server 控制通道超时: %w", dialCtx.Err())
		case <-ticker.C:
		}
	}
}

func (c *ScrcpyControlClient) InstanceID() string { return c.id }
func (c *ScrcpyControlClient) ScreenSize() (int, int) {
	return c.width, c.height
}

func (c *ScrcpyControlClient) writePacket(pkt []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return errors.New("控制通道已关闭")
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err := c.conn.Write(pkt)
	return err
}

func (c *ScrcpyControlClient) SendTouch(action uint8, rx, ry float64, jitterPx int) error {
	x, y := DenormalizePoint(rx, ry, c.width, c.height, jitterPx, cryptoRandIntn)
	pressure := uint16(0xffff)
	actionBtn := int32(ButtonPrimary)
	buttons := int32(ButtonPrimary)
	if action == ActionUp {
		pressure = 0
		buttons = 0
	} else if action == ActionMove {
		actionBtn = 0
	}
	pkt := EncodeTouch(action, PointerIDGenericFinger, x, y, uint16(c.width), uint16(c.height), pressure, actionBtn, buttons)
	return c.writePacket(pkt)
}

func (c *ScrcpyControlClient) SendScroll(rx, ry float64, hScroll, vScroll float32) error {
	x, y := DenormalizePoint(rx, ry, c.width, c.height, 0, nil)
	pkt := EncodeScroll(x, y, uint16(c.width), uint16(c.height), hScroll, vScroll, 0)
	return c.writePacket(pkt)
}

func (c *ScrcpyControlClient) SendKey(keycode uint32) error {
	down := EncodeKeycode(ActionDown, keycode, 0, 0)
	up := EncodeKeycode(ActionUp, keycode, 0, 0)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return errors.New("控制通道已关闭")
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.conn.Write(down); err != nil {
		return err
	}
	_, err := c.conn.Write(up)
	return err
}

func (c *ScrcpyControlClient) SendText(text string) error {
	if text == "" {
		return nil
	}
	return c.writePacket(EncodeSetClipboard(c.clipSeq.Add(1), true, text))
}

func (c *ScrcpyControlClient) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		c.writeMu.Lock()
		if c.conn != nil {
			closeErr = c.conn.Close()
			c.conn = nil
		}
		c.writeMu.Unlock()
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		fctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = runADB(fctx, c.adbBin, c.adbEnv, "-s", fmt.Sprintf("emulator-%d", c.port), "forward", "--remove", fmt.Sprintf("tcp:%d", c.localPort))
	})
	return closeErr
}

func runADB(ctx context.Context, adbBin string, adbEnv []string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, adbBin, args...)
	if len(adbEnv) > 0 {
		cmd.Env = adbEnv
	}
	hideConsole(cmd)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func reserveLocalPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func randomPositiveSCID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	v := binary.BigEndian.Uint32(b[:]) & 0x7FFFFFFF
	if v == 0 {
		v = 1
	}
	return fmt.Sprintf("%08x", v)
}

func cryptoRandIntn(n int) int {
	if n <= 1 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}

type limitedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buf) < 4096 {
		remain := 4096 - len(l.buf)
		if len(p) > remain {
			l.buf = append(l.buf, p[:remain]...)
		} else {
			l.buf = append(l.buf, p...)
		}
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.buf)
}

package emu

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"virdeck/config"
)

// IsSOCKS5Proxy reports whether raw specifies a socks5:// or socks5h:// proxy URL.
func IsSOCKS5Proxy(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(s, "socks5://") || strings.HasPrefix(s, "socks5h://")
}

// SOCKSBridge exposes a loopback HTTP CONNECT proxy forwarding TCP connections through an upstream SOCKS5 server.
type SOCKSBridge struct {
	listener    net.Listener
	server      *http.Server
	proc        *os.Process
	addr        string
	closeMu     sync.Once
	connMu      sync.Mutex
	closed      bool
	activeConns map[net.Conn]struct{}
	sem         chan struct{}
}

func (b *SOCKSBridge) trackConn(c net.Conn, add bool) {
	if b == nil || c == nil {
		return
	}
	b.connMu.Lock()
	defer b.connMu.Unlock()
	if add {
		if b.closed {
			_ = c.Close()
			return
		}
		b.activeConns[c] = struct{}{}
	} else {
		delete(b.activeConns, c)
	}
}

func validateSOCKSBridgeURL(socksURL string) (upstreamAddr, username, password string, err error) {
	u, err := url.Parse(strings.TrimSpace(socksURL))
	if err != nil {
		return "", "", "", fmt.Errorf("解析 SOCKS5 代理地址 %q 失败: %s", config.RedactProxyURL(socksURL), config.RedactError(err))
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "socks5" && scheme != "socks5h" {
		return "", "", "", fmt.Errorf("不支持的代理协议: %s", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return "", "", "", errors.New("SOCKS5 代理缺少主机地址")
	}
	port := u.Port()
	if port == "" {
		port = "1080"
	}
	upstreamAddr = net.JoinHostPort(host, port)
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}
	return upstreamAddr, username, password, nil
}

// StartSOCKSBridge launches a local loopback HTTP-to-SOCKS5 TCP bridge for emulator -http-proxy.
func StartSOCKSBridge(socksURL string) (*SOCKSBridge, error) {
	upstreamAddr, username, password, err := validateSOCKSBridgeURL(socksURL)
	if err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("启动本地 SOCKS5 桥接监听失败: %w", err)
	}

	dialTarget := func(ctx context.Context, network, targetAddr string) (net.Conn, error) {
		if !strings.HasPrefix(network, "tcp") {
			return nil, fmt.Errorf("本地 SOCKS5 桥接仅支持 TCP，不支持 %s", network)
		}
		return dialSOCKS5(ctx, upstreamAddr, username, password, targetAddr)
	}

	plainTransport := &http.Transport{
		DialContext:           dialTarget,
		ResponseHeaderTimeout: 30 * time.Second,
	}

	b := &SOCKSBridge{
		listener:    ln,
		addr:        ln.Addr().String(),
		activeConns: make(map[net.Conn]struct{}),
		sem:         make(chan struct{}, 256),
	}
	b.server = &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case b.sem <- struct{}{}:
			default:
				http.Error(w, "too many proxy connections", http.StatusServiceUnavailable)
				return
			}
			if r.Method == http.MethodConnect {
				b.handleConnectTunnel(w, r, dialTarget)
				return
			}
			defer func() { <-b.sem }()
			b.handleHTTPForward(w, r, plainTransport)
		}),
	}
	go func() {
		_ = b.server.Serve(ln)
	}()
	return b, nil
}

// HTTPProxyURL returns the local HTTP proxy URL to pass to emulator -http-proxy.
func (b *SOCKSBridge) HTTPProxyURL() string {
	if b == nil || b.addr == "" {
		return ""
	}
	return "http://" + b.addr
}

// Close shuts down the loopback SOCKS5 bridge server and listener (or kills a detached helper if startup aborts).
func (b *SOCKSBridge) Close() error {
	if b == nil {
		return nil
	}
	var err error
	b.closeMu.Do(func() {
		b.connMu.Lock()
		b.closed = true
		conns := make([]net.Conn, 0, len(b.activeConns))
		for c := range b.activeConns {
			conns = append(conns, c)
		}
		b.activeConns = nil
		b.connMu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
		if b.proc != nil {
			err = b.proc.Kill()
			return
		}
		if b.server != nil {
			err = b.server.Close()
		} else if b.listener != nil {
			err = b.listener.Close()
		}
	})
	return err
}

// StartDetachedSOCKSBridge spawns a detached background helper process running "__socks-bridge"
// so CLI-started emulators retain their local HTTP->SOCKS5 bridge after the CLI command exits.
func StartDetachedSOCKSBridge(exePath string, extraArgs []string, socksURL string, watchPort int) (*SOCKSBridge, error) {
	if _, _, _, err := validateSOCKSBridgeURL(socksURL); err != nil {
		return nil, err
	}
	if watchPort <= 0 {
		return nil, fmt.Errorf("无效的监视端口: %d", watchPort)
	}
	bin := strings.TrimSpace(exePath)
	if bin == "" {
		resolved, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("无法定位当前可执行文件: %w", err)
		}
		bin = resolved
	}

	args := append(append([]string(nil), extraArgs...), "__socks-bridge", "-watch-port", strconv.Itoa(watchPort))
	cmd := exec.Command(bin, args...)
	detachBackgroundProcess(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动后台 SOCKS5 桥接进程失败: %w", err)
	}

	_, writeErr := io.WriteString(stdin, strings.TrimSpace(socksURL)+"\n")
	_ = stdin.Close()
	if writeErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("写入 SOCKS5 代理配置失败: %w", writeErr)
	}

	type lineResult struct {
		line string
		err  error
	}
	lineCh := make(chan lineResult, 1)
	go func() {
		r := bufio.NewReader(stdout)
		line, readErr := r.ReadString('\n')
		lineCh <- lineResult{line: line, err: readErr}
	}()

	select {
	case res := <-lineCh:
		if res.err != nil && strings.TrimSpace(res.line) == "" {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			detail := strings.TrimSpace(errBuf.String())
			if detail != "" {
				return nil, fmt.Errorf("后台 SOCKS5 桥接启动失败: %s", detail)
			}
			return nil, fmt.Errorf("读取后台 SOCKS5 桥接端口失败: %w", res.err)
		}
		trimmed := strings.TrimSpace(res.line)
		if !strings.HasPrefix(trimmed, "READY ") {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, fmt.Errorf("后台 SOCKS5 桥接返回异常输出: %q", trimmed)
		}
		addr := strings.TrimSpace(strings.TrimPrefix(trimmed, "READY "))
		if addr == "" {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, errors.New("后台 SOCKS5 桥接未返回监听地址")
		}
		return &SOCKSBridge{
			addr: addr,
			proc: cmd.Process,
		}, nil
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New("等待后台 SOCKS5 桥接就绪超时")
	}
}

// RunSOCKSBridgeHelper runs the detached SOCKS5 bridge lifecycle until the emulator port closes or OS signal arrives.
func RunSOCKSBridgeHelper(watchPort int, stdin io.Reader, stdout io.Writer) error {
	return RunSOCKSBridgeHelperWithTimings(watchPort, stdin, stdout, 45*time.Second, 5*time.Second)
}

// RunSOCKSBridgeHelperWithTimings starts the SOCKS5 bridge using stdin URL and exits cleanly
// once 127.0.0.1:<watchPort> fails 3 consecutive probes after grace.
func RunSOCKSBridgeHelperWithTimings(watchPort int, stdin io.Reader, stdout io.Writer, grace, pollInterval time.Duration) error {
	if watchPort <= 0 {
		return fmt.Errorf("无效的监视端口: %d", watchPort)
	}
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	reader := bufio.NewReader(io.LimitReader(stdin, 4096))
	line, err := reader.ReadString('\n')
	if err != nil && len(strings.TrimSpace(line)) == 0 {
		return fmt.Errorf("读取代理配置失败: %w", err)
	}
	bridge, err := StartSOCKSBridge(strings.TrimSpace(line))
	if err != nil {
		return err
	}
	defer func() { _ = bridge.Close() }()

	if _, err := fmt.Fprintf(stdout, "READY %s\n", bridge.addr); err != nil {
		return err
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	if grace > 0 {
		timer := time.NewTimer(grace)
		select {
		case <-sigCh:
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(watchPort))
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	misses := 0
	for {
		conn, dialErr := net.DialTimeout("tcp", addr, time.Second)
		if dialErr == nil {
			_ = conn.Close()
			misses = 0
		} else {
			misses++
			if misses >= 3 {
				return nil
			}
		}
		select {
		case <-sigCh:
			return nil
		case <-ticker.C:
		}
	}
}

func (b *SOCKSBridge) handleConnectTunnel(w http.ResponseWriter, r *http.Request, dialFn func(context.Context, string, string) (net.Conn, error)) {
	releaseSem := true
	defer func() {
		if releaseSem {
			<-b.sem
		}
	}()
	target := r.Host
	if target == "" {
		target = r.URL.Host
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(target, "443")
	}
	if strings.EqualFold(target, b.addr) {
		http.Error(w, "loopback self-proxy rejected", http.StatusForbidden)
		return
	}
	upstream, err := dialFn(r.Context(), "tcp", target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, bufRW, err := hj.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = upstream.Close()
		_ = clientConn.Close()
		return
	}
	if bufRW != nil && bufRW.Reader.Buffered() > 0 {
		if _, err := io.CopyN(upstream, bufRW.Reader, int64(bufRW.Reader.Buffered())); err != nil {
			_ = upstream.Close()
			_ = clientConn.Close()
			return
		}
	}
	b.trackConn(clientConn, true)
	b.trackConn(upstream, true)
	releaseSem = false
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, clientConn)
		_ = upstream.Close()
		b.trackConn(upstream, false)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, upstream)
		_ = clientConn.Close()
		b.trackConn(clientConn, false)
	}()
	go func() {
		wg.Wait()
		<-b.sem
	}()
}

var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Keep-Alive",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func stripHopByHopHeaders(h http.Header) {
	for _, connVal := range h.Values("Connection") {
		for _, field := range strings.Split(connVal, ",") {
			if name := strings.TrimSpace(field); name != "" {
				h.Del(name)
			}
		}
	}
	for _, hdr := range hopByHopHeaders {
		h.Del(hdr)
	}
}

func (b *SOCKSBridge) handleHTTPForward(w http.ResponseWriter, r *http.Request, tr *http.Transport) {
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	if outReq.URL.Scheme == "" {
		outReq.URL.Scheme = "http"
	}
	if outReq.URL.Host == "" {
		outReq.URL.Host = r.Host
	}
	if strings.EqualFold(outReq.URL.Host, b.addr) {
		http.Error(w, "loopback self-proxy rejected", http.StatusForbidden)
		return
	}
	stripHopByHopHeaders(outReq.Header)
	resp, err := tr.RoundTrip(outReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	stripHopByHopHeaders(resp.Header)
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func dialSOCKS5(ctx context.Context, proxyAddr, user, pass, targetAddr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = conn.Close()
		}
	}()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	}

	// Greeting (RFC 1928)
	var methods []byte
	if user != "" || pass != "" {
		methods = []byte{0x05, 0x02, 0x00, 0x02}
	} else {
		methods = []byte{0x05, 0x01, 0x00}
	}
	if _, err := conn.Write(methods); err != nil {
		return nil, err
	}
	var choice [2]byte
	if _, err := io.ReadFull(conn, choice[:]); err != nil {
		return nil, err
	}
	if choice[0] != 0x05 {
		return nil, fmt.Errorf("SOCKS5 协议版本异常: %d", choice[0])
	}
	switch choice[1] {
	case 0x00:
		// No auth required.
	case 0x02:
		if len(user) > 255 || len(pass) > 255 {
			return nil, errors.New("SOCKS5 用户名或密码过长")
		}
		auth := make([]byte, 0, 3+len(user)+len(pass))
		auth = append(auth, 0x01, byte(len(user)))
		auth = append(auth, user...)
		auth = append(auth, byte(len(pass)))
		auth = append(auth, pass...)
		if _, err := conn.Write(auth); err != nil {
			return nil, err
		}
		var authResp [2]byte
		if _, err := io.ReadFull(conn, authResp[:]); err != nil {
			return nil, err
		}
		if authResp[0] != 0x01 || authResp[1] != 0x00 {
			return nil, errors.New("SOCKS5 账号密码认证失败")
		}
	default:
		return nil, fmt.Errorf("SOCKS5 服务端拒绝支持的认证方式: %d", choice[1])
	}

	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return nil, err
	}
	portNum, err := strconv.Atoi(portStr)
	if err != nil || portNum < 1 || portNum > 65535 {
		return nil, fmt.Errorf("无效的目标端口: %s", portStr)
	}

	req := make([]byte, 0, 10+len(host))
	req = append(req, 0x05, 0x01, 0x00)
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(req, 0x01)
			req = append(req, ip4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return nil, errors.New("SOCKS5 目标域名过长")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	var portBuf [2]byte
	binary.BigEndian.PutUint16(portBuf[:], uint16(portNum))
	req = append(req, portBuf[:]...)
	if _, err := conn.Write(req); err != nil {
		return nil, err
	}

	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return nil, err
	}
	if hdr[0] != 0x05 {
		return nil, fmt.Errorf("SOCKS5 响应版本异常: %d", hdr[0])
	}
	if hdr[1] != 0x00 {
		return nil, fmt.Errorf("SOCKS5 连接目标失败 (rep=%d)", hdr[1])
	}

	var drainLen int
	switch hdr[3] {
	case 0x01:
		drainLen = 4 + 2
	case 0x04:
		drainLen = 16 + 2
	case 0x03:
		var ln [1]byte
		if _, err := io.ReadFull(conn, ln[:]); err != nil {
			return nil, err
		}
		drainLen = int(ln[0]) + 2
	default:
		return nil, fmt.Errorf("SOCKS5 未知地址类型: %d", hdr[3])
	}
	if _, err := io.CopyN(io.Discard, conn, int64(drainLen)); err != nil {
		return nil, err
	}

	_ = conn.SetDeadline(time.Time{})
	success = true
	return conn, nil
}

package emu

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestIsSOCKS5Proxy(t *testing.T) {
	for _, yes := range []string{"socks5://127.0.0.1:1080", "SOCKS5H://user:pass@proxy:1080"} {
		if !IsSOCKS5Proxy(yes) {
			t.Fatalf("expected IsSOCKS5Proxy(%q) = true", yes)
		}
	}
	for _, no := range []string{"", "http://127.0.0.1:7890", "https://127.0.0.1:7890"} {
		if IsSOCKS5Proxy(no) {
			t.Fatalf("expected IsSOCKS5Proxy(%q) = false", no)
		}
	}
}

func TestSOCKSBridgeForwardsHTTPAndCONNECTWithAuth(t *testing.T) {
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello-via-socks5")
	}))
	defer targetSrv.Close()

	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socksLn.Close()

	go serveMockSOCKS5(t, socksLn, "u1", "p1")

	bridge, err := StartSOCKSBridge("socks5://u1:p1@" + socksLn.Addr().String())
	if err != nil {
		t.Fatalf("StartSOCKSBridge: %v", err)
	}
	defer bridge.Close()

	proxyURL, err := url.Parse(bridge.HTTPProxyURL())
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := hc.Get(targetSrv.URL + "/ping")
	if err != nil {
		t.Fatalf("HTTP via SOCKSBridge failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "hello-via-socks5" {
		t.Fatalf("unexpected HTTP response: %q", string(body))
	}

	// Verify CONNECT tunneling through the bridge as well.
	conn, err := net.Dial("tcp", proxyURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tu, _ := url.Parse(targetSrv.URL)
	_, _ = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", tu.Host, tu.Host)
	buf := make([]byte, 128)
	n, err := conn.Read(buf)
	if err != nil || !contains200(string(buf[:n])) {
		t.Fatalf("CONNECT response = %q err=%v", string(buf[:n]), err)
	}
}

func contains200(s string) bool {
	return len(s) >= 12 && (s[:12] == "HTTP/1.1 200" || s[:12] == "HTTP/1.0 200")
}

func serveMockSOCKS5(t *testing.T, ln net.Listener, wantUser, wantPass string) {
	t.Helper()
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(conn net.Conn) {
			defer conn.Close()
			var hdr [2]byte
			if _, err := io.ReadFull(conn, hdr[:]); err != nil {
				return
			}
			methods := make([]byte, hdr[1])
			if _, err := io.ReadFull(conn, methods); err != nil {
				return
			}
			if _, err := conn.Write([]byte{0x05, 0x02}); err != nil {
				return
			}
			var authVer [2]byte
			if _, err := io.ReadFull(conn, authVer[:]); err != nil {
				return
			}
			u := make([]byte, authVer[1])
			if _, err := io.ReadFull(conn, u); err != nil {
				return
			}
			var plen [1]byte
			if _, err := io.ReadFull(conn, plen[:]); err != nil {
				return
			}
			p := make([]byte, plen[0])
			if _, err := io.ReadFull(conn, p); err != nil {
				return
			}
			if string(u) != wantUser || string(p) != wantPass {
				_, _ = conn.Write([]byte{0x01, 0x01})
				return
			}
			_, _ = conn.Write([]byte{0x01, 0x00})

			var req [4]byte
			if _, err := io.ReadFull(conn, req[:]); err != nil {
				return
			}
			var host string
			switch req[3] {
			case 0x01:
				var ip [4]byte
				_, _ = io.ReadFull(conn, ip[:])
				host = net.IP(ip[:]).String()
			case 0x03:
				var lnBuf [1]byte
				_, _ = io.ReadFull(conn, lnBuf[:])
				name := make([]byte, lnBuf[0])
				_, _ = io.ReadFull(conn, name)
				host = string(name)
			default:
				return
			}
			var portBuf [2]byte
			_, _ = io.ReadFull(conn, portBuf[:])
			port := binary.BigEndian.Uint16(portBuf[:])

			up, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
			if err != nil {
				_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
				return
			}
			defer up.Close()
			_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 0})
			go func() {
				_, _ = io.Copy(up, conn)
				_ = up.Close()
			}()
			_, _ = io.Copy(conn, up)
		}(c)
	}
}

func TestStartSOCKSBridgeRedactsPasswordOnParseError(t *testing.T) {
	_, err := StartSOCKSBridge("socks5://user:SuperSecretPass@127.0.0.1:badport")
	if err == nil {
		t.Fatal("expected error for malformed socks5 port")
	}
	if containsString(err.Error(), "SuperSecretPass") || !containsString(err.Error(), "user:***@127.0.0.1:badport") {
		t.Fatalf("StartSOCKSBridge leaked proxy password: %v", err)
	}
}

func containsString(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexString(s, sub) >= 0)
}

func indexString(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestRunSOCKSBridgeHelperProxiesAndExitsWhenWatchPortCloses(t *testing.T) {
	// Upstream SOCKS5 server.
	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socksLn.Close()
	go serveMockSOCKS5(t, socksLn, "u1", "p1")

	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "detached-ok")
	}))
	defer targetSrv.Close()

	// Watchable dummy console port.
	watchLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	watchPort := watchLn.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			c, aerr := watchLn.Accept()
			if aerr != nil {
				return
			}
			_ = c.Close()
		}
	}()

	pr, pw := io.Pipe()
	helperErr := make(chan error, 1)
	go func() {
		in := strings.NewReader("socks5://u1:p1@" + socksLn.Addr().String() + "\n")
		helperErr <- RunSOCKSBridgeHelperWithTimings(watchPort, in, pw, 0, 15*time.Millisecond)
		_ = pw.Close()
	}()

	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatalf("read READY line failed: %v", err)
	}
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "READY ") {
		t.Fatalf("unexpected helper output: %q", trimmed)
	}
	proxyAddr := strings.TrimSpace(strings.TrimPrefix(trimmed, "READY "))
	proxyURL, err := url.Parse("http://" + proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := hc.Get(targetSrv.URL)
	if err != nil {
		t.Fatalf("HTTP through helper failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "detached-ok" {
		t.Fatalf("unexpected helper proxy body: %q", string(body))
	}

	// Closing watchLn simulates emulator exit; helper should exit cleanly after 3 misses.
	_ = watchLn.Close()
	select {
	case err := <-helperErr:
		if err != nil {
			t.Fatalf("helper returned error on watch exit: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("helper did not exit after watchPort closed")
	}
	_ = bytes.MinRead
}

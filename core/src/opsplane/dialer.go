/*
 * Copyright 2026 Clidey, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package opsplane

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProxyConfig describes an ops-control-plane environment's network proxy settings.
type ProxyConfig struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	SSHKey   string `json:"ssh_key"`
}

var (
	proxyRoutesMu     sync.RWMutex
	proxyRoutesByAddr = map[string]ProxyConfig{}
	proxyRoutesByHost = map[string]ProxyConfig{}
	proxyRoutesByEnv  = map[string]ProxyConfig{}
)

// RegisterEnvironmentProxy records the proxy configuration associated with an environment slug.
func RegisterEnvironmentProxy(envSlug string, proxy ProxyConfig) {
	trimmedSlug := strings.TrimSpace(envSlug)
	if trimmedSlug == "" {
		return
	}
	proxyRoutesMu.Lock()
	defer proxyRoutesMu.Unlock()
	proxyRoutesByEnv[trimmedSlug] = proxy
}

// RegisterEnvironmentTargetProxy associates a target database host (and optional port) with the proxy configured for envSlug.
func RegisterEnvironmentTargetProxy(envSlug string, host string, port string) {
	trimmedSlug := strings.TrimSpace(envSlug)
	if trimmedSlug == "" {
		return
	}
	proxyRoutesMu.RLock()
	proxy, ok := proxyRoutesByEnv[trimmedSlug]
	proxyRoutesMu.RUnlock()
	if !ok {
		return
	}
	RegisterTargetProxy(host, port, proxy)
}

// RegisterTargetProxy associates a target database host (and optional port) with an environment proxy.
func RegisterTargetProxy(host string, port string, proxy ProxyConfig) {
	trimmedHost := strings.TrimSpace(host)
	if trimmedHost == "" {
		return
	}
	proxyType := strings.ToLower(strings.TrimSpace(proxy.Type))
	if proxyType == "" || proxyType == "direct" || strings.TrimSpace(proxy.Host) == "" || proxy.Port <= 0 {
		return
	}

	proxyRoutesMu.Lock()
	defer proxyRoutesMu.Unlock()
	if p := strings.TrimSpace(port); p != "" {
		proxyRoutesByAddr[net.JoinHostPort(trimmedHost, p)] = proxy
	}
	proxyRoutesByHost[trimmedHost] = proxy
}

// LookupTargetProxy resolves the proxy configuration for a target address, if any.
func LookupTargetProxy(addr string) (ProxyConfig, bool) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if shouldBypassProxy(host) {
		return ProxyConfig{}, false
	}

	proxyRoutesMu.RLock()
	defer proxyRoutesMu.RUnlock()
	if proxy, ok := proxyRoutesByAddr[addr]; ok {
		return proxy, true
	}
	if proxy, ok := proxyRoutesByHost[host]; ok {
		return proxy, true
	}
	return ProxyConfig{}, false
}

// shouldBypassProxy matches ops-control-plane's direct-routing rule for loopback and Tailscale/WireGuard 100.64.0.0/24 addresses.
func shouldBypassProxy(host string) bool {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" || strings.EqualFold(trimmed, "localhost") || trimmed == "127.0.0.1" || trimmed == "::1" {
		return true
	}
	return strings.HasPrefix(trimmed, "100.64.0.")
}

// DialContext dials target database addresses directly or through the environment's configured proxy.
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	directDialer := &net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	proxy, ok := LookupTargetProxy(addr)
	if !ok {
		return directDialer.DialContext(ctx, network, addr)
	}

	switch strings.ToLower(strings.TrimSpace(proxy.Type)) {
	case "socks5":
		return dialSOCKS5(ctx, directDialer, proxy, addr)
	case "http":
		return dialHTTPConnect(ctx, directDialer, proxy, addr)
	default:
		return directDialer.DialContext(ctx, network, addr)
	}
}

func dialSOCKS5(ctx context.Context, dialer *net.Dialer, proxy ProxyConfig, targetAddr string) (net.Conn, error) {
	targetHost, targetPortStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid target address %q: %w", targetAddr, err)
	}
	targetPort, err := strconv.Atoi(targetPortStr)
	if err != nil || targetPort < 1 || targetPort > 65535 {
		return nil, fmt.Errorf("invalid target port %q", targetPortStr)
	}

	proxyAddr := net.JoinHostPort(strings.TrimSpace(proxy.Host), strconv.Itoa(proxy.Port))
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("dial socks5 proxy %s: %w", proxyAddr, err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	}
	defer func() {
		_ = conn.SetDeadline(time.Time{})
	}()

	hasAuth := strings.TrimSpace(proxy.Username) != "" || proxy.Password != ""
	var greeting []byte
	if hasAuth {
		greeting = []byte{0x05, 0x02, 0x00, 0x02}
	} else {
		greeting = []byte{0x05, 0x01, 0x00}
	}
	if _, err := conn.Write(greeting); err != nil {
		_ = conn.Close()
		return nil, err
	}

	methodResp := make([]byte, 2)
	if _, err := io.ReadFull(conn, methodResp); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if methodResp[0] != 0x05 {
		_ = conn.Close()
		return nil, fmt.Errorf("unexpected socks5 version %d", methodResp[0])
	}

	switch methodResp[1] {
	case 0x00:
		// No authentication required.
	case 0x02:
		userBytes := []byte(proxy.Username)
		passBytes := []byte(proxy.Password)
		if len(userBytes) > 255 || len(passBytes) > 255 {
			_ = conn.Close()
			return nil, errors.New("socks5 credentials exceed 255 bytes")
		}
		authReq := make([]byte, 0, 3+len(userBytes)+len(passBytes))
		authReq = append(authReq, 0x01, byte(len(userBytes)))
		authReq = append(authReq, userBytes...)
		authReq = append(authReq, byte(len(passBytes)))
		authReq = append(authReq, passBytes...)
		if _, err := conn.Write(authReq); err != nil {
			_ = conn.Close()
			return nil, err
		}
		authResp := make([]byte, 2)
		if _, err := io.ReadFull(conn, authResp); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if authResp[1] != 0x00 {
			_ = conn.Close()
			return nil, errors.New("socks5 proxy authentication failed")
		}
	default:
		_ = conn.Close()
		return nil, fmt.Errorf("unsupported socks5 auth method %d", methodResp[1])
	}

	connectReq := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(targetHost); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			connectReq = append(connectReq, 0x01)
			connectReq = append(connectReq, ip4...)
		} else {
			connectReq = append(connectReq, 0x04)
			connectReq = append(connectReq, ip.To16()...)
		}
	} else {
		hostBytes := []byte(targetHost)
		if len(hostBytes) > 255 {
			_ = conn.Close()
			return nil, errors.New("socks5 target hostname too long")
		}
		connectReq = append(connectReq, 0x03, byte(len(hostBytes)))
		connectReq = append(connectReq, hostBytes...)
	}
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(targetPort))
	connectReq = append(connectReq, portBytes...)

	if _, err := conn.Write(connectReq); err != nil {
		_ = conn.Close()
		return nil, err
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if header[1] != 0x00 {
		_ = conn.Close()
		return nil, fmt.Errorf("socks5 connect to %s failed with reply code %d", targetAddr, header[1])
	}

	var discardLen int
	switch header[3] {
	case 0x01:
		discardLen = 4 + 2
	case 0x04:
		discardLen = 16 + 2
	case 0x03:
		lenByte := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenByte); err != nil {
			_ = conn.Close()
			return nil, err
		}
		discardLen = int(lenByte[0]) + 2
	default:
		_ = conn.Close()
		return nil, fmt.Errorf("unknown socks5 address type %d", header[3])
	}

	if discardLen > 0 {
		if _, err := io.CopyN(io.Discard, conn, int64(discardLen)); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}

	return conn, nil
}

func dialHTTPConnect(ctx context.Context, dialer *net.Dialer, proxy ProxyConfig, targetAddr string) (net.Conn, error) {
	proxyAddr := net.JoinHostPort(strings.TrimSpace(proxy.Host), strconv.Itoa(proxy.Port))
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("dial http proxy %s: %w", proxyAddr, err)
	}

	var reqBuilder strings.Builder
	reqBuilder.WriteString(fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", targetAddr, targetAddr))
	if strings.TrimSpace(proxy.Username) != "" || proxy.Password != "" {
		token := base64.StdEncoding.EncodeToString([]byte(proxy.Username + ":" + proxy.Password))
		reqBuilder.WriteString("Proxy-Authorization: Basic " + token + "\r\n")
	}
	reqBuilder.WriteString("\r\n")

	if _, err := io.WriteString(conn, reqBuilder.String()); err != nil {
		_ = conn.Close()
		return nil, err
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("http proxy connect failed: %s", resp.Status)
	}
	return conn, nil
}

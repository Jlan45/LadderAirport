package frpsruntime

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	adminHost              = "127.0.0.1"
	adminResponseLimit     = 4 << 20
	adminCredentialBytes   = 24
	adminStartupRetryCount = 4
)

var (
	proxyTypes      = []string{"tcp", "udp", "http", "https", "tcpmux", "stcp", "xtcp", "sudp"}
	adminHTTPClient = &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout: 2 * time.Second,
			}).DialContext,
		},
	}
)

type adminEndpoint struct {
	host     string
	port     int
	username string
	password string
}

func (a adminEndpoint) baseURL() string {
	return "http://" + net.JoinHostPort(a.host, strconv.Itoa(a.port))
}

type Client struct {
	Key             string
	User            string
	ClientID        string
	RunID           string
	Version         string
	WireProtocol    string
	Hostname        string
	ClientIP        string
	ConnectedAtUnix int64
	Online          bool
}

type Mapping struct {
	Name               string
	Type               string
	Status             string
	User               string
	ClientID           string
	LocalIP            string
	LocalPort          int
	RemotePort         int
	CustomDomains      []string
	Subdomain          string
	CurrentConnections int64
	TrafficInBytes     int64
	TrafficOutBytes    int64
	LastStartTime      string
	Plugin             string
}

type MappingSnapshot struct {
	Clients         []Client
	Mappings        []Mapping
	CollectedAtUnix int64
}

type adminClient struct {
	Key             string `json:"key"`
	User            string `json:"user"`
	ClientID        string `json:"clientID"`
	RunID           string `json:"runID"`
	Version         string `json:"version"`
	WireProtocol    string `json:"wireProtocol"`
	Hostname        string `json:"hostname"`
	ClientIP        string `json:"clientIP"`
	LastConnectedAt int64  `json:"lastConnectedAt"`
	Online          bool   `json:"online"`
}

type adminProxyList struct {
	Proxies []adminProxy `json:"proxies"`
}

type adminProxy struct {
	Name            string           `json:"name"`
	Conf            adminProxyConfig `json:"conf"`
	User            string           `json:"user"`
	ClientID        string           `json:"clientID"`
	TodayTrafficIn  int64            `json:"todayTrafficIn"`
	TodayTrafficOut int64            `json:"todayTrafficOut"`
	CurConns        int64            `json:"curConns"`
	LastStartTime   string           `json:"lastStartTime"`
	Status          string           `json:"status"`
}

type adminProxyConfig struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	LocalIP       string   `json:"localIP"`
	LocalPort     int      `json:"localPort"`
	RemotePort    int      `json:"remotePort"`
	CustomDomains []string `json:"customDomains"`
	Subdomain     string   `json:"subdomain"`
	Plugin        struct {
		Type string `json:"type"`
	} `json:"plugin"`
}

func newAdminEndpoint() (adminEndpoint, error) {
	listener, err := net.Listen("tcp", net.JoinHostPort(adminHost, "0"))
	if err != nil {
		return adminEndpoint{}, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return adminEndpoint{}, err
	}
	username, err := randomAdminCredential()
	if err != nil {
		return adminEndpoint{}, err
	}
	password, err := randomAdminCredential()
	if err != nil {
		return adminEndpoint{}, err
	}
	return adminEndpoint{
		host: adminHost, port: port, username: username, password: password,
	}, nil
}

func randomAdminCredential() (string, error) {
	raw := make([]byte, adminCredentialBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (r *Runtime) Mappings(ctx context.Context) (MappingSnapshot, error) {
	r.mu.Lock()
	if r.state != StateRunning || r.service == nil || r.admin.port == 0 {
		r.mu.Unlock()
		return MappingSnapshot{}, fmt.Errorf("FRPS 当前未运行")
	}
	admin := r.admin
	r.mu.Unlock()

	var rawClients []adminClient
	if err := getAdminJSONWithRetry(ctx, admin, "/api/clients?status=online", &rawClients); err != nil {
		return MappingSnapshot{}, fmt.Errorf("读取 FRPS 在线客户端失败：%w", err)
	}

	snapshot := MappingSnapshot{
		Clients:         make([]Client, 0, len(rawClients)),
		Mappings:        []Mapping{},
		CollectedAtUnix: time.Now().Unix(),
	}
	for _, item := range rawClients {
		if !item.Online {
			continue
		}
		snapshot.Clients = append(snapshot.Clients, Client{
			Key: item.Key, User: item.User, ClientID: item.ClientID,
			RunID: item.RunID, Version: item.Version, WireProtocol: item.WireProtocol,
			Hostname: item.Hostname, ClientIP: item.ClientIP,
			ConnectedAtUnix: item.LastConnectedAt, Online: item.Online,
		})
	}

	for _, proxyType := range proxyTypes {
		var response adminProxyList
		path := "/api/proxy/" + url.PathEscape(proxyType)
		if err := getAdminJSONWithRetry(ctx, admin, path, &response); err != nil {
			return MappingSnapshot{}, fmt.Errorf("读取 FRPS %s 映射失败：%w", proxyType, err)
		}
		for _, item := range response.Proxies {
			if !strings.EqualFold(item.Status, "online") {
				continue
			}
			resolvedType := item.Conf.Type
			if resolvedType == "" {
				resolvedType = proxyType
			}
			snapshot.Mappings = append(snapshot.Mappings, Mapping{
				Name: item.Name, Type: resolvedType, Status: item.Status,
				User: item.User, ClientID: item.ClientID,
				LocalIP: item.Conf.LocalIP, LocalPort: item.Conf.LocalPort,
				RemotePort:         item.Conf.RemotePort,
				CustomDomains:      append([]string{}, item.Conf.CustomDomains...),
				Subdomain:          item.Conf.Subdomain,
				CurrentConnections: item.CurConns,
				TrafficInBytes:     item.TodayTrafficIn, TrafficOutBytes: item.TodayTrafficOut,
				LastStartTime: item.LastStartTime, Plugin: item.Conf.Plugin.Type,
			})
		}
	}
	sort.Slice(snapshot.Clients, func(i, j int) bool {
		left := snapshot.Clients[i].ClientID
		if left == "" {
			left = snapshot.Clients[i].RunID
		}
		right := snapshot.Clients[j].ClientID
		if right == "" {
			right = snapshot.Clients[j].RunID
		}
		return left < right
	})
	sort.Slice(snapshot.Mappings, func(i, j int) bool {
		if snapshot.Mappings[i].Type == snapshot.Mappings[j].Type {
			return snapshot.Mappings[i].Name < snapshot.Mappings[j].Name
		}
		return snapshot.Mappings[i].Type < snapshot.Mappings[j].Type
	})
	return snapshot, nil
}

func getAdminJSONWithRetry(ctx context.Context, admin adminEndpoint, path string, target any) error {
	var lastErr error
	for attempt := 0; attempt < adminStartupRetryCount; attempt++ {
		lastErr = getAdminJSON(ctx, admin, path, target)
		if lastErr == nil {
			return nil
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 40 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

func getAdminJSON(ctx context.Context, admin adminEndpoint, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, admin.baseURL()+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(admin.username, admin.password)
	resp, err := adminHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, adminResponseLimit))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("解析响应失败：%w", err)
	}
	return nil
}

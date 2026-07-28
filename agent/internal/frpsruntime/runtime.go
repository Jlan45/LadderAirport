package frpsruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	frpversion "github.com/fatedier/frp/pkg/util/version"
	"github.com/fatedier/frp/server"
)

type State string

const (
	StateStopped State = "stopped"
	StateRunning State = "running"
	StateError   State = "error"
)

func Version() string {
	return frpversion.Full()
}

type Status struct {
	State         State
	ConfigHash    string
	StartedAtUnix int64
	LastError     string
}

type persistedConfig struct {
	Config     *Config `json:"config"`
	ConfigHash string  `json:"config_hash"`
}

type Runtime struct {
	applyMu sync.Mutex
	mu      sync.Mutex

	dataDir string
	service *server.Service
	cancel  context.CancelFunc
	admin   adminEndpoint

	config        Config
	configHash    string
	startedAtUnix int64
	lastError     string
	state         State
}

func New(dataDir string) *Runtime {
	return &Runtime{
		dataDir: dataDir,
		state:   StateStopped,
	}
}

func (r *Runtime) Apply(ctx context.Context, config Config, hash string) error {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	return r.applyLocked(ctx, config, hash)
}

func (r *Runtime) applyLocked(ctx context.Context, config Config, hash string) error {
	config = config.Normalize()
	if err := config.Validate(); err != nil {
		r.setLastError(err.Error())
		return err
	}
	canonical, err := config.canonicalJSON()
	if err != nil {
		return fmt.Errorf("编码 FRPS 配置失败：%w", err)
	}
	if hash == "" {
		sum := sha256.Sum256(canonical)
		hash = hex.EncodeToString(sum[:])
	}

	r.mu.Lock()
	same := r.state == StateRunning && r.service != nil &&
		config.Enabled && hash == r.configHash
	previousConfig, previousHash := r.config, r.configHash
	r.mu.Unlock()
	if same {
		return nil
	}

	r.stopServiceLocked()
	if !config.Enabled {
		r.mu.Lock()
		r.config = config
		r.configHash = hash
		r.lastError = ""
		r.state = StateStopped
		r.mu.Unlock()
		return r.writeCurrent(config, hash)
	}

	if err := r.startServiceLocked(ctx, config, hash); err != nil {
		if previousConfig.Enabled && previousHash != "" {
			if restoreErr := r.startServiceLocked(context.Background(), previousConfig, previousHash); restoreErr != nil {
				message := fmt.Sprintf("启动 FRPS 失败：%v；恢复旧配置也失败：%v", err, restoreErr)
				r.setLastError(message)
				return fmt.Errorf("%s", message)
			}
			message := "启动 FRPS 失败，已恢复旧配置：" + err.Error()
			r.setLastError(message)
			return fmt.Errorf("%s", message)
		}
		r.setLastError(err.Error())
		return err
	}

	if err := r.writeCurrent(config, hash); err != nil {
		r.setLastError("FRPS 正在运行，但保存配置失败：" + err.Error())
	}
	return nil
}

func (r *Runtime) startServiceLocked(ctx context.Context, config Config, hash string) error {
	cfg, err := config.serverConfig()
	if err != nil {
		return err
	}
	admin, err := newAdminEndpoint()
	if err != nil {
		return fmt.Errorf("初始化 FRPS 本机管理面失败：%w", err)
	}
	cfg.WebServer.Addr = admin.host
	cfg.WebServer.Port = admin.port
	cfg.WebServer.User = admin.username
	cfg.WebServer.Password = admin.password
	service, err := server.NewService(cfg)
	if err != nil {
		return fmt.Errorf("创建 FRPS 服务失败：%w", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())

	r.mu.Lock()
	r.service = service
	r.cancel = cancel
	r.admin = admin
	r.config = config
	r.configHash = hash
	r.startedAtUnix = time.Now().Unix()
	r.lastError = ""
	r.state = StateRunning
	r.mu.Unlock()

	go func(current *server.Service) {
		current.Run(runCtx)
		r.mu.Lock()
		if r.service == current {
			r.service = nil
			r.cancel = nil
			r.admin = adminEndpoint{}
			r.startedAtUnix = 0
			if runCtx.Err() == nil {
				r.state = StateError
				r.lastError = "FRPS 服务意外退出"
			} else {
				r.state = StateStopped
			}
		}
		r.mu.Unlock()
	}(service)

	select {
	case <-ctx.Done():
		r.stopServiceLocked()
		return ctx.Err()
	default:
		return nil
	}
}

func (r *Runtime) Start(ctx context.Context) error {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()

	r.mu.Lock()
	if r.state == StateRunning && r.service != nil {
		r.mu.Unlock()
		return nil
	}
	config, hash := r.config, r.configHash
	r.mu.Unlock()
	if !config.Enabled || hash == "" {
		return fmt.Errorf("没有可启动的 FRPS 配置")
	}
	return r.startServiceLocked(ctx, config, hash)
}

func (r *Runtime) Stop(context.Context) error {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	r.stopServiceLocked()
	return nil
}

func (r *Runtime) stopServiceLocked() {
	r.mu.Lock()
	service, cancel := r.service, r.cancel
	r.service = nil
	r.cancel = nil
	r.admin = adminEndpoint{}
	r.startedAtUnix = 0
	r.state = StateStopped
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if service != nil {
		_ = service.Close()
	}
}

func (r *Runtime) Status(context.Context) Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Status{
		State:         r.state,
		ConfigHash:    r.configHash,
		StartedAtUnix: r.startedAtUnix,
		LastError:     r.lastError,
	}
}

func (r *Runtime) Restore(ctx context.Context) error {
	if r.dataDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(r.dataDir, "frps-current.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 FRPS 缓存配置失败：%w", err)
	}
	var persisted persistedConfig
	if err := json.Unmarshal(data, &persisted); err == nil && persisted.Config != nil {
		return r.Apply(ctx, *persisted.Config, persisted.ConfigHash)
	}
	// Backward compatibility with the initial cache format, which stored the
	// Config object directly and derived a new hash during restore.
	var legacy Config
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("解析 FRPS 缓存配置失败：%w", err)
	}
	return r.Apply(ctx, legacy, "")
}

func (r *Runtime) setLastError(message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastError = message
	if r.service == nil {
		r.state = StateError
	}
}

func (r *Runtime) writeCurrent(config Config, hash string) error {
	if r.dataDir == "" {
		return nil
	}
	data, err := json.Marshal(persistedConfig{Config: &config, ConfigHash: hash})
	if err != nil {
		return fmt.Errorf("编码 FRPS 缓存配置失败：%w", err)
	}
	if err := os.MkdirAll(r.dataDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(r.dataDir, "frps-current.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

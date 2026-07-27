package dnsprovider

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type registration struct {
	metadata Metadata
	factory  Factory
}

type Registry struct {
	mu      sync.RWMutex
	entries map[string]registration
}

func NewRegistry() *Registry {
	return &Registry{entries: map[string]registration{}}
}

func (r *Registry) Register(metadata Metadata, factory Factory) error {
	if r == nil {
		return fmt.Errorf("DNS 供应商注册表为空")
	}
	name := strings.ToLower(strings.TrimSpace(metadata.Name))
	if name == "" || factory == nil {
		return fmt.Errorf("DNS 供应商名称和工厂不能为空")
	}
	metadata.Name = name
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[name]; exists {
		return fmt.Errorf("DNS 供应商已注册：%s", name)
	}
	r.entries[name] = registration{metadata: metadata, factory: factory}
	return nil
}

func (r *Registry) New(name string, config Config) (Provider, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	r.mu.RLock()
	entry, exists := r.entries[name]
	r.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("不支持 DNS 供应商：%s", name)
	}
	provider, err := entry.factory(config)
	if err != nil {
		return nil, fmt.Errorf("初始化 DNS 供应商 %s 失败：%w", name, err)
	}
	return provider, nil
}

func (r *Registry) Metadata() []Metadata {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Metadata, 0, len(r.entries))
	for _, entry := range r.entries {
		out = append(out, entry.metadata)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

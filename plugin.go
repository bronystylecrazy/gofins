package fins

import (
	"fmt"
	"sync"
)

// Plugin allows extending client behavior (logging, metrics, tracing, etc.).
// Inspired by gorm's plugin model.
type Plugin interface {
	// Name must return a unique plugin name.
	Name() string
	// Initialize is called once when the plugin is registered via Use.
	Initialize(*Client) error
}

// pluginManager wraps plugin registration to keep the Client struct focused.
type pluginManager struct {
	mu      sync.Mutex
	plugins map[string]Plugin
}

func (pm *pluginManager) use(c *Client, plugins ...Plugin) error {
	for _, p := range plugins {
		if p == nil {
			return fmt.Errorf("plugin is nil")
		}
		name := p.Name()
		if name == "" {
			return fmt.Errorf("plugin name cannot be empty")
		}

		// Reserve the name to avoid duplicate registration races.
		pm.mu.Lock()
		if pm.plugins == nil {
			pm.plugins = make(map[string]Plugin)
		}
		if _, exists := pm.plugins[name]; exists {
			pm.mu.Unlock()
			return fmt.Errorf("plugin %s already registered", name)
		}
		pm.plugins[name] = nil
		pm.mu.Unlock()

		if err := p.Initialize(c); err != nil {
			pm.mu.Lock()
			delete(pm.plugins, name)
			pm.mu.Unlock()
			return fmt.Errorf("initialize plugin %s: %w", name, err)
		}

		pm.mu.Lock()
		pm.plugins[name] = p
		pm.mu.Unlock()
	}

	return nil
}

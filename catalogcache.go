package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"smovie/internal/hongguo"
)

// catalogTTL is how long a category's first page stays before it loads
// again.
const catalogTTL = 30 * time.Minute

func catalogPath(key string) string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	if configOverride != "" {
		base = configOverride // tests
	}
	return filepath.Join(base, "smovie", "catalog", key+".json")
}

// saveCatalog keeps the first page of a category, to show at the next
// launch before it loads; call on the main thread with a page no longer
// changed.
func saveCatalog(key string, p *hongguo.Page) {
	go func() {
		data, err := json.Marshal(p)
		if err != nil {
			return
		}
		path := catalogPath(key)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if os.WriteFile(path+".tmp", data, 0o644) == nil {
			_ = os.Rename(path+".tmp", path)
		}
	}()
}

// loadCatalog reads what saveCatalog kept, however old: it shows only
// while the category loads.
func loadCatalog(key string) (*hongguo.Page, bool) {
	data, err := os.ReadFile(catalogPath(key))
	if err != nil {
		return nil, false
	}
	var p hongguo.Page
	if json.Unmarshal(data, &p) != nil || len(p.Items) == 0 {
		return nil, false
	}
	return &p, true
}

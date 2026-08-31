// Package models cung cấp registry metadata model LLM gồm context window, giới hạn output và giá.
// Nguồn dữ liệu là OpenRouter API, với baseline lúc biên dịch và làm mới runtime.
package models

//go:generate go run gen_models.go

import (
	"strings"
	"sync"
)

// ModelEntry mô tả một model LLM đã biết.
type ModelEntry struct {
	Provider            string  `json:"provider"`               // Tên vendor đã chuẩn hóa từ OpenRouter (anthropic/openai/gemini/...)
	ID                  string  `json:"id"`                     // Model ID không gồm tiền tố vendor
	Name                string  `json:"name"`                   // Tên hiển thị
	ContextWindow       int     `json:"context_window"`         // Cửa sổ input
	MaxTokens           int     `json:"max_tokens"`             // Giới hạn output mỗi lần
	InputCostPer1M      float64 `json:"input_cost_per_1m"`      // Giá input (USD/1M tokens)
	OutputCostPer1M     float64 `json:"output_cost_per_1m"`     // Giá output
	CacheReadCostPer1M  float64 `json:"cache_read_cost_per_1m"` // Giá đọc cache
	CacheWriteCostPer1M float64 `json:"cache_write_cost_per_1m"`
}

// ModelRegistry lưu các model đã biết, hỗ trợ resolve mờ và merge runtime.
type ModelRegistry struct {
	mu     sync.RWMutex
	models []ModelEntry
}

// NewModelRegistry trả về registry đã nạp baseline lúc biên dịch.
func NewModelRegistry() *ModelRegistry {
	r := &ModelRegistry{}
	r.models = append(r.models, generatedModels...)
	return r
}

var (
	defaultRegistry     *ModelRegistry
	defaultRegistryOnce sync.Once
)

// DefaultRegistry trả về registry toàn cục, lazy-load và an toàn luồng.
// Gọi StartPricingRefresh ở giai đoạn khởi động để nền làm mới giá và thông tin window.
func DefaultRegistry() *ModelRegistry {
	defaultRegistryOnce.Do(func() {
		defaultRegistry = NewModelRegistry()
	})
	return defaultRegistry
}

// Resolve tìm entry theo một định danh model, có thể là "provider/model", ID đầy đủ hoặc tên cục bộ.
//
// Thứ tự khớp:
//  1. Nếu chứa "/", tìm chính xác theo "provider/model"
//  2. Khớp chính xác hoặc khớp hậu tố ngày
//  3. Khớp chuỗi con khi ID hoặc Name chứa pattern
//
// Khi có nhiều kết quả, ưu tiên alias không có hậu tố ngày, ví dụ claude-sonnet-4 trước claude-sonnet-4-20250514.
func (r *ModelRegistry) Resolve(pattern string) (*ModelEntry, bool) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	if idx := strings.Index(pattern, "/"); idx > 0 {
		prov := pattern[:idx]
		modelID := pattern[idx+1:]
		if entry, ok := lookupModelEntry(r.models, prov, modelID); ok {
			return &entry, true
		}
		// Tiền tố vendor của OpenRouter như google/ hoặc x-ai/ không nhất thiết bằng tên Provider cục bộ,
		// nên fallback sang tra bằng modelID để "google/gemini-2.5-pro" vẫn trúng entry gemini.
		if entry, ok := lookupModelEntry(r.models, "", modelID); ok {
			return &entry, true
		}
	}

	if entry, ok := lookupModelEntry(r.models, "", pattern); ok {
		return &entry, true
	}

	lower := strings.ToLower(pattern)
	normalized := normalizeModelLookupID(pattern)
	var candidates []int
	for i := range r.models {
		if strings.Contains(normalizeModelLookupID(r.models[i].ID), normalized) ||
			strings.Contains(strings.ToLower(r.models[i].ID), lower) ||
			strings.Contains(strings.ToLower(r.models[i].Name), lower) {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return nil, false
	}

	best := candidates[0]
	for _, i := range candidates[1:] {
		if !hasDatedSuffix(r.models[i].ID) && hasDatedSuffix(r.models[best].ID) {
			best = i
		}
	}
	entry := r.models[best]
	return &entry, true
}

// ResolveContextWindow trả về context window của một model; không trúng thì trả 0.
func (r *ModelRegistry) ResolveContextWindow(pattern string) int {
	if e, ok := r.Resolve(pattern); ok {
		return e.ContextWindow
	}
	return 0
}

// List trả về mọi model; filter tùy chọn, chuỗi rỗng nghĩa là toàn bộ.
func (r *ModelRegistry) List(filter string) []ModelEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if filter == "" {
		return append([]ModelEntry{}, r.models...)
	}
	lower := strings.ToLower(filter)
	normalized := normalizeModelLookupID(filter)
	var out []ModelEntry
	for _, m := range r.models {
		if strings.Contains(strings.ToLower(m.Provider), lower) ||
			strings.Contains(normalizeModelLookupID(m.ID), normalized) ||
			strings.Contains(strings.ToLower(m.ID), lower) ||
			strings.Contains(strings.ToLower(m.Name), lower) {
			out = append(out, m)
		}
	}
	return out
}

// MergeModels merge không phân biệt hoa/thường theo provider+id.
// Giá, window, MaxTokens hoặc Name khác zero sẽ ghi đè mục hiện có; mục mới được append trực tiếp.
func (r *ModelRegistry) MergeModels(fetched []ModelEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	idx := make(map[string]int, len(r.models))
	for i, m := range r.models {
		idx[strings.ToLower(m.Provider+"/"+m.ID)] = i
	}
	for _, f := range fetched {
		key := strings.ToLower(f.Provider + "/" + f.ID)
		if i, ok := idx[key]; ok {
			if f.InputCostPer1M > 0 || f.OutputCostPer1M > 0 {
				r.models[i].InputCostPer1M = f.InputCostPer1M
				r.models[i].OutputCostPer1M = f.OutputCostPer1M
				r.models[i].CacheReadCostPer1M = f.CacheReadCostPer1M
				r.models[i].CacheWriteCostPer1M = f.CacheWriteCostPer1M
			}
			if f.ContextWindow > 0 {
				r.models[i].ContextWindow = f.ContextWindow
			}
			if f.MaxTokens > 0 {
				r.models[i].MaxTokens = f.MaxTokens
			}
			if f.Name != "" {
				r.models[i].Name = f.Name
			}
		} else {
			r.models = append(r.models, f)
			idx[key] = len(r.models) - 1
		}
	}
}

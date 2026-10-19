package ai

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

// ModelCacheTTL is how long the model list is considered fresh.
const ModelCacheTTL = time.Hour

// Jev model ids (OpenRouter Decisions API).
const (
	JevModel       = "typesafe/jev-1.13"
	JevLatestModel = "~typesafe/jev-latest"
)

// IsDecisionModel reports whether id is a Decisions API model.
func IsDecisionModel(id string) bool {
	id = strings.TrimPrefix(strings.ToLower(id), "~")
	return strings.HasPrefix(id, "typesafe/jev")
}

type modelCache struct {
	mu        sync.Mutex
	file      string
	fetchedAt time.Time
	list      []models.AIModel
}

type cacheFile struct {
	FetchedAt time.Time        `json:"fetchedAt"`
	Models    []models.AIModel `json:"models"`
}

func (m *modelCache) load() {
	if m.file == "" || m.list != nil {
		return
	}
	data, err := os.ReadFile(m.file)
	if err != nil {
		return
	}
	var cf cacheFile
	if json.Unmarshal(data, &cf) == nil && len(cf.Models) > 0 {
		m.list, m.fetchedAt = cf.Models, cf.FetchedAt
	}
}

func (m *modelCache) save() {
	if m.file == "" {
		return
	}
	data, err := json.Marshal(cacheFile{FetchedAt: m.fetchedAt, Models: m.list})
	if err != nil {
		return
	}
	tmp := m.file + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, m.file)
	}
}

type orModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	ContextLength int    `json:"context_length"`
	Pricing       struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
}

// perMillion converts an OpenRouter per-token USD price string to USD per
// 1M tokens. ok is false for missing, negative ("-1" = variable) or
// unparseable prices.
func perMillion(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return math.Round(f*1e6*1e6) / 1e6, true
}

func convertModel(m orModel) models.AIModel {
	out := models.AIModel{ID: m.ID, Name: m.Name, Description: m.Description, ContextLength: m.ContextLength, Kind: "chat"}
	if out.Name == "" {
		out.Name = m.ID
	}
	p, ok1 := perMillion(m.Pricing.Prompt)
	c, ok2 := perMillion(m.Pricing.Completion)
	out.PromptPrice, out.CompletionPrice = p, c
	out.PriceUnknown = !ok1 || !ok2
	if IsDecisionModel(m.ID) {
		out.Kind = "decision"
		out.PriceUnknown = !ok1 // decisions are billed on input tokens only
	}
	return out
}

func jevEntries() []models.AIModel {
	return []models.AIModel{
		{ID: JevModel, Name: "TypeSafe: Jev 1.13", Kind: "decision", PriceUnknown: true,
			Description: "Decision model for classification via the OpenRouter Decisions API (pinned version). Billed on input tokens."},
		{ID: JevLatestModel, Name: "TypeSafe: Jev (latest)", Kind: "decision", PriceUnknown: true,
			Description: "Alias for the newest Jev decision model via the OpenRouter Decisions API. Billed on input tokens."},
	}
}

// ListModels returns all OpenRouter models sorted by name. The list is
// cached in memory and on disk for an hour; force refetches. /models needs
// no API key. Jev entries are added when OpenRouter doesn't list them.
func (c *Client) ListModels(ctx context.Context, force bool) ([]models.AIModel, error) {
	mc := c.models
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.load()
	if !force && mc.list != nil && time.Since(mc.fetchedAt) < ModelCacheTTL {
		return clone(mc.list), nil
	}
	api, _ := c.api(false)
	if key, err := c.key(); err == nil && key != "" {
		api, _ = c.api(true)
	}
	var resp struct {
		Data []orModel `json:"data"`
	}
	if err := api.JSON(ctx, http.MethodGet, "v1/models", nil, &resp); err != nil {
		if mc.list != nil {
			return clone(mc.list), nil // stale but usable
		}
		return nil, friendly(err)
	}
	list := make([]models.AIModel, 0, len(resp.Data)+2)
	seen := map[string]bool{}
	for _, m := range resp.Data {
		if m.ID == "" || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		list = append(list, convertModel(m))
	}
	for _, j := range jevEntries() {
		if !seen[j.ID] {
			list = append(list, j)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := strings.ToLower(list[i].Name), strings.ToLower(list[j].Name)
		if a != b {
			return a < b
		}
		return list[i].ID < list[j].ID
	})
	mc.list, mc.fetchedAt = list, time.Now()
	mc.save()
	return clone(list), nil
}

// Model looks up one model from the (cached) list.
func (c *Client) Model(ctx context.Context, id string) (models.AIModel, bool) {
	list, err := c.ListModels(ctx, false)
	if err != nil {
		return models.AIModel{}, false
	}
	for _, m := range list {
		if m.ID == id {
			return m, true
		}
	}
	return models.AIModel{}, false
}

func clone(l []models.AIModel) []models.AIModel { return append([]models.AIModel(nil), l...) }

// CacheFile is the default model cache path inside dataDir.
func CacheFile(dataDir string) string { return filepath.Join(dataDir, "openrouter-models.json") }

// TestKey validates the API key.
func (c *Client) TestKey(ctx context.Context) error {
	api, err := c.api(true)
	if err != nil {
		return err
	}
	var resp struct {
		Data struct {
			Label string `json:"label"`
		} `json:"data"`
	}
	err = api.JSON(ctx, http.MethodGet, "v1/key", nil, &resp)
	if err != nil && strings.Contains(err.Error(), "not found") {
		err = api.JSON(ctx, http.MethodGet, "v1/auth/key", nil, &resp)
	}
	return friendly(err)
}

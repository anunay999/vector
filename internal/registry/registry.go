// Package registry indexes the configured models and resolves the namespaced
// identifiers used by roles and policies. It is deliberately separate from
// routing: the router asks the registry "what can serve this target?", and the
// registry answers with provider + upstream model + capability tags.
package registry

import (
	"sort"
	"strings"

	"github.com/anunay999/vector/internal/config"
)

// Entry is a resolved, routable model.
type Entry struct {
	ID         string       `json:"id"`
	ProviderID string       `json:"provider"`
	Upstream   string       `json:"upstream"`
	Tags       []string     `json:"tags"`
	Context    int          `json:"context"`
	Price      config.Price `json:"price"`
}

// HasTag reports whether the entry carries tag (case-insensitive).
func (e Entry) HasTag(tag string) bool {
	for _, t := range e.Tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

// Registry is an immutable view over configured models.
type Registry struct {
	entries []Entry
	byID    map[string]Entry
	byProv  map[string][]Entry
}

// New builds a registry from configuration.
func New(cfg *config.Config) *Registry {
	r := &Registry{
		byID:   map[string]Entry{},
		byProv: map[string][]Entry{},
	}
	for _, m := range cfg.Models {
		providerID, upstream, ok := strings.Cut(m.ID, "/")
		if !ok {
			continue
		}
		e := Entry{
			ID:         m.ID,
			ProviderID: providerID,
			Upstream:   upstream,
			Tags:       append([]string(nil), m.Tags...),
			Context:    m.Context,
			Price:      m.Price,
		}
		r.entries = append(r.entries, e)
		r.byID[e.ID] = e
		r.byProv[providerID] = append(r.byProv[providerID], e)
	}
	sort.Slice(r.entries, func(i, j int) bool { return r.entries[i].ID < r.entries[j].ID })
	return r
}

// Lookup returns the entry for a namespaced id.
func (r *Registry) Lookup(id string) (Entry, bool) {
	e, ok := r.byID[id]
	return e, ok
}

// EntryForProvider returns the lowest-priced entry for a provider, if any.
func (r *Registry) EntryForProvider(providerID string) (Entry, bool) {
	list := r.byProv[providerID]
	if len(list) == 0 {
		return Entry{}, false
	}
	best := list[0]
	for _, e := range list[1:] {
		if e.Price.In+e.Price.Out < best.Price.In+best.Price.Out {
			best = e
		}
	}
	return best, true
}

// Entries returns all entries in stable order.
func (r *Registry) Entries() []Entry { return append([]Entry(nil), r.entries...) }

// MissingTags returns the tags in required that the entry does not carry.
func (e Entry) MissingTags(required []string) []string {
	var missing []string
	for _, t := range required {
		if !e.HasTag(t) {
			missing = append(missing, t)
		}
	}
	return missing
}

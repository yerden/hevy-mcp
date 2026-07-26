// Package exercises exposes an embedded, pre-fetched snapshot of Hevy's
// public exercise-template catalog for fast local lookup by title substring,
// muscle group, or exercise type. Custom (per-user) exercises are intentionally
// out of scope — clients should combine this with the live
// hevy_list_exercise_templates tool if they need those.
//
// The snapshot lives at catalog.json but the repo only tracks a `[]` stub —
// the real catalog is populated at Docker build time via a BuildKit secret and
// baked into the binary. Local dev can regenerate it manually by running
// `go generate ./internal/exercises/...` (requires HEVY_API_KEY).
package exercises

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yerden/hevy-mcp/internal/hevy"
)

//go:generate go run ./gen

//go:embed catalog.json
var catalogJSON []byte

// Catalog is the parsed embedded snapshot.
var Catalog []hevy.ExerciseTemplate

func init() {
	if err := json.Unmarshal(catalogJSON, &Catalog); err != nil {
		panic(fmt.Errorf("exercises: parse embedded catalog: %w", err))
	}
}

// Size returns the number of entries in the embedded catalog. Zero means the
// binary was built without a populated catalog (build-time secret was missing
// or generation was skipped). Callers should surface this in a startup log so
// an empty search tool is easy to diagnose.
func Size() int { return len(Catalog) }

// SearchParams filters the catalog. All filters are optional and combine with
// AND semantics; if none are set, the first Limit entries are returned.
type SearchParams struct {
	Query             string // substring match on title, case-insensitive
	MuscleGroup       string // matches primary OR secondary muscle group (exact, case-insensitive)
	MuscleGroupPrimaryOnly bool // when true, MuscleGroup only matches the primary muscle group
	ExerciseType      string // exact match on template type (case-insensitive)
	Limit             int    // cap on returned entries; 0 or negative = no cap
}

// Search returns catalog entries matching every non-empty filter, up to Limit.
// Order is stable — matches the catalog's on-disk order (sorted by template ID).
func Search(p SearchParams) []hevy.ExerciseTemplate {
	q := strings.ToLower(strings.TrimSpace(p.Query))
	mg := strings.ToLower(strings.TrimSpace(p.MuscleGroup))
	et := strings.ToLower(strings.TrimSpace(p.ExerciseType))

	out := make([]hevy.ExerciseTemplate, 0, 32)
	for _, t := range Catalog {
		if q != "" && !strings.Contains(strings.ToLower(t.Title), q) {
			continue
		}
		if mg != "" && !matchMuscleGroup(t, mg, p.MuscleGroupPrimaryOnly) {
			continue
		}
		if et != "" && !strings.EqualFold(string(t.Type), et) {
			continue
		}
		out = append(out, t)
		if p.Limit > 0 && len(out) >= p.Limit {
			break
		}
	}
	return out
}

func matchMuscleGroup(t hevy.ExerciseTemplate, mg string, primaryOnly bool) bool {
	if strings.EqualFold(string(t.PrimaryMuscleGroup), mg) {
		return true
	}
	if primaryOnly {
		return false
	}
	for _, s := range t.SecondaryMuscleGroups {
		if strings.EqualFold(string(s), mg) {
			return true
		}
	}
	return false
}

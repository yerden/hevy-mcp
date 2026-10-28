// Package exercises searches Hevy's exercise-template catalog by title
// substring, muscle group, or exercise type.
//
// There is no local snapshot: each search pages through the whole of GET
// /v1/exercise_templates at that endpoint's 100-per-page ceiling and filters
// on the fly. Scanning to the end (rather than stopping at the first N hits)
// lets callers report a true total, which is what nudges a model to page
// through the rest. Results always reflect the live catalog, and include the
// caller's own custom exercises alongside the built-in ones.
package exercises

import (
	"strings"

	"github.com/yerden/hevy-mcp/internal/hevy"
)

const (
	// pageSize is /v1/exercise_templates' documented (and live-verified)
	// maximum — the one Hevy list endpoint that allows more than 10. The full
	// catalog (~460 entries) is about 5 requests at this size.
	pageSize = 100
	// maxPages bounds Hevy traffic per search regardless of what page_count
	// claims: 2000 templates is ~4x the current catalog.
	maxPages = 20
)

// Lister fetches one page of exercise templates. *hevy.Client satisfies it.
type Lister interface {
	ListExerciseTemplates(page, pageSize int) (*hevy.PaginatedExerciseTemplates, error)
}

// SearchParams filters the catalog. All filters are optional and combine with
// AND semantics; if none are set, the whole catalog matches.
type SearchParams struct {
	Query                  string // substring match on title, case-insensitive
	MuscleGroup            string // matches primary OR secondary muscle group (exact, case-insensitive)
	MuscleGroupPrimaryOnly bool   // when true, MuscleGroup only matches the primary muscle group
	ExerciseType           string // exact match on template type (case-insensitive)
}

// Search pages through the catalog via l and returns every entry matching all
// non-empty filters, in Hevy's listing order. Any page error aborts the
// search — a partial result would look like "no such exercise".
func Search(l Lister, p SearchParams) ([]hevy.ExerciseTemplate, error) {
	q := strings.ToLower(strings.TrimSpace(p.Query))
	mg := strings.ToLower(strings.TrimSpace(p.MuscleGroup))
	et := strings.ToLower(strings.TrimSpace(p.ExerciseType))

	out := make([]hevy.ExerciseTemplate, 0, 32)
	for page := 1; page <= maxPages; page++ {
		resp, err := l.ListExerciseTemplates(page, pageSize)
		if err != nil {
			return nil, err
		}
		for _, t := range resp.ExerciseTemplates {
			if q != "" && !strings.Contains(strings.ToLower(t.Title), q) {
				continue
			}
			if mg != "" && !matchMuscleGroup(t, mg, p.MuscleGroupPrimaryOnly) {
				continue
			}
			if et != "" && !strings.EqualFold(string(t.Type), et) {
				continue
			}
			// Thumbnails are ~110-character S3 URLs of no use to a model
			// reading search results. `equipment` is kept: it disambiguates
			// "Bench Press (Barbell)" vs the machine variant.
			t.ThumbnailURL = ""
			out = append(out, t)
		}
		if page >= resp.PageCount || len(resp.ExerciseTemplates) == 0 {
			break
		}
	}
	return out, nil
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

package exercises

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yerden/hevy-mcp/internal/hevy"
)

// fakeLister serves entries in pages of size `per`, ignoring the requested
// pageSize so tests can exercise multi-page walks with a handful of rows.
type fakeLister struct {
	entries   []hevy.ExerciseTemplate
	per       int
	pageCount int // overrides the computed page_count when non-zero
	failPage  int
	calls     []int // page numbers requested, in order
	sizes     []int // pageSize requested, in order
}

func (f *fakeLister) ListExerciseTemplates(page, pageSize int) (*hevy.PaginatedExerciseTemplates, error) {
	f.calls = append(f.calls, page)
	f.sizes = append(f.sizes, pageSize)
	if page == f.failPage {
		return nil, errors.New("hevy: HTTP 429")
	}
	per := f.per
	if per == 0 {
		per = len(f.entries)
	}
	count := 1
	if per > 0 {
		count = (len(f.entries) + per - 1) / per
	}
	if f.pageCount != 0 {
		count = f.pageCount
	}
	out := &hevy.PaginatedExerciseTemplates{Page: page, PageCount: count, ExerciseTemplates: []hevy.ExerciseTemplate{}}
	lo := (page - 1) * per
	if lo < len(f.entries) {
		out.ExerciseTemplates = f.entries[lo:min(lo+per, len(f.entries))]
	}
	return out, nil
}

func fixture() []hevy.ExerciseTemplate {
	return []hevy.ExerciseTemplate{
		{ID: "01A35BF9", Title: "EZ Bar Biceps Curl", Type: "weight_reps", PrimaryMuscleGroup: "biceps"},
		{ID: "05293BCA", Title: "Back Squat (Barbell)", Type: "weight_reps", PrimaryMuscleGroup: "quadriceps", SecondaryMuscleGroups: []hevy.MuscleGroup{"glutes", "hamstrings"}},
		{ID: "0F1D2E3A", Title: "Bulgarian Split Squat", Type: "weight_reps", PrimaryMuscleGroup: "quadriceps", SecondaryMuscleGroups: []hevy.MuscleGroup{"glutes"}},
		{ID: "1A2B3C4D", Title: "Hammer Curl (Dumbbell)", Type: "weight_reps", PrimaryMuscleGroup: "biceps", SecondaryMuscleGroups: []hevy.MuscleGroup{"forearms"}},
		{ID: "2B3C4D5E", Title: "Rowing Machine", Type: "distance_duration", PrimaryMuscleGroup: "cardio"},
	}
}

func search(t *testing.T, entries []hevy.ExerciseTemplate, p SearchParams) []hevy.ExerciseTemplate {
	t.Helper()
	got, err := Search(&fakeLister{entries: entries, per: 2}, p)
	require.NoError(t, err)
	return got
}

func TestSearch_QueryMatchesTitleSubstringCaseInsensitive(t *testing.T) {
	got := search(t, fixture(), SearchParams{Query: "squat"})
	require.Len(t, got, 2)
	for _, e := range got {
		assert.Contains(t, strings.ToLower(e.Title), "squat")
	}
}

func TestSearch_MuscleGroupMatchesPrimaryOrSecondary(t *testing.T) {
	got := search(t, fixture(), SearchParams{MuscleGroup: "glutes"})
	// Back Squat (secondary) and Bulgarian Split Squat (secondary)
	require.Len(t, got, 2)
	for _, e := range got {
		assert.Contains(t, e.SecondaryMuscleGroups, hevy.MuscleGroup("glutes"))
	}
}

func TestSearch_MuscleGroupPrimaryOnly(t *testing.T) {
	// A synthetic entry with biceps as *secondary* proves primary_only
	// filters it out.
	entries := append(fixture(), hevy.ExerciseTemplate{
		ID: "ZZZZZZ", Title: "Pretend Row (Barbell)", Type: "weight_reps",
		PrimaryMuscleGroup:    "upper_back",
		SecondaryMuscleGroups: []hevy.MuscleGroup{"biceps"},
	})

	full := search(t, entries, SearchParams{MuscleGroup: "biceps"})
	primary := search(t, entries, SearchParams{MuscleGroup: "biceps", MuscleGroupPrimaryOnly: true})

	require.Len(t, full, 3)
	require.Len(t, primary, 2)
	for _, e := range primary {
		assert.Equal(t, "biceps", string(e.PrimaryMuscleGroup),
			"primary_only must exclude %s (primary=%s)", e.Title, e.PrimaryMuscleGroup)
	}
	assert.Equal(t, "ZZZZZZ", full[2].ID, "full match should include the secondary-biceps row")
}

func TestSearch_FiltersCombineWithAnd(t *testing.T) {
	got := search(t, fixture(), SearchParams{Query: "curl", MuscleGroup: "biceps"})
	require.Len(t, got, 2)
	for _, e := range got {
		assert.Contains(t, strings.ToLower(e.Title), "curl")
	}
}

func TestSearch_UnknownMuscleGroupReturnsEmpty(t *testing.T) {
	got := search(t, fixture(), SearchParams{MuscleGroup: "no_such_muscle"})
	assert.Empty(t, got)
}

func TestSearch_ExerciseTypeFilter(t *testing.T) {
	got := search(t, fixture(), SearchParams{ExerciseType: "distance_duration"})
	require.Len(t, got, 1)
	assert.Equal(t, "Rowing Machine", got[0].Title)
}

func TestSearch_EmptyCatalogReturnsEmpty(t *testing.T) {
	got := search(t, nil, SearchParams{Query: "anything"})
	assert.NotNil(t, got, "an empty result must marshal as [], not null")
	assert.Empty(t, got)
}

// Hevy's ceiling for this endpoint is 100; asking for less multiplies traffic.
func TestSearch_RequestsMaxPageSize(t *testing.T) {
	f := &fakeLister{entries: fixture()}
	_, err := Search(f, SearchParams{})
	require.NoError(t, err)
	assert.Equal(t, []int{100}, f.sizes)
}

// The whole catalog is scanned so the caller can report a true total, and the
// walk ends at page_count rather than probing past it.
func TestSearch_WalksAllPagesUpToPageCount(t *testing.T) {
	f := &fakeLister{entries: fixture(), per: 2}
	got, err := Search(f, SearchParams{MuscleGroup: "biceps"})
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, f.calls)
	require.Len(t, got, 2)
}

func TestSearch_PageCountIsCappedAtMaxPages(t *testing.T) {
	// page_count claims far more pages than exist, and every page is
	// non-empty: only maxPages bounds the walk.
	entries := make([]hevy.ExerciseTemplate, 50)
	f := &fakeLister{entries: entries, per: 1, pageCount: 1000}
	_, err := Search(f, SearchParams{Query: "never matches"})
	require.NoError(t, err)
	assert.Len(t, f.calls, maxPages)
}

func TestSearch_EmptyPageEndsWalk(t *testing.T) {
	f := &fakeLister{entries: fixture(), per: 5, pageCount: 10}
	_, err := Search(f, SearchParams{Query: "never matches"})
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, f.calls)
}

func TestSearch_PageErrorAbortsSearch(t *testing.T) {
	f := &fakeLister{entries: fixture(), per: 2, failPage: 2}
	got, err := Search(f, SearchParams{Query: "squat"})
	require.Error(t, err)
	assert.Empty(t, got, "a partial result must not be returned")
}

func TestSearch_DropsThumbnailURL(t *testing.T) {
	entries := []hevy.ExerciseTemplate{{ID: "A", Title: "Squat", ThumbnailURL: "https://s3/x.jpg", Equipment: "barbell"}}
	got := search(t, entries, SearchParams{})
	require.Len(t, got, 1)
	assert.Empty(t, got[0].ThumbnailURL)
	assert.Equal(t, hevy.EquipmentCategory("barbell"), got[0].Equipment)
}

func TestSearch_IncludesCustomExercises(t *testing.T) {
	entries := []hevy.ExerciseTemplate{{ID: "c1", Title: "My Squat", IsCustom: true}}
	got := search(t, entries, SearchParams{Query: "squat"})
	require.Len(t, got, 1)
	assert.True(t, got[0].IsCustom)
}

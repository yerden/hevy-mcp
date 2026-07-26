package exercises

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yerden/hevy-mcp/internal/hevy"
)

// withFixture swaps Catalog for the duration of a test. The stub committed to
// the repo is `[]`, so tests can't rely on real data — every test that needs
// entries must seed them here.
func withFixture(t *testing.T, entries []hevy.ExerciseTemplate) {
	t.Helper()
	prev := Catalog
	Catalog = entries
	t.Cleanup(func() { Catalog = prev })
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

func TestCatalog_EmbeddedStubParsesToEmptySlice(t *testing.T) {
	// The committed stub is `[]`; init() must accept it without panicking.
	// Populated catalogs are the deploy-time artifact, not a repo asset.
	assert.NotNil(t, Catalog, "init must always leave Catalog non-nil")
}

func TestSearch_QueryMatchesTitleSubstringCaseInsensitive(t *testing.T) {
	withFixture(t, fixture())
	got := Search(SearchParams{Query: "squat", Limit: 10})
	require.Len(t, got, 2)
	for _, e := range got {
		assert.Contains(t, strings.ToLower(e.Title), "squat")
	}
}

func TestSearch_MuscleGroupMatchesPrimaryOrSecondary(t *testing.T) {
	withFixture(t, fixture())
	got := Search(SearchParams{MuscleGroup: "glutes", Limit: 10})
	// Back Squat (secondary) and Bulgarian Split Squat (secondary)
	require.Len(t, got, 2)
	for _, e := range got {
		found := false
		for _, s := range e.SecondaryMuscleGroups {
			if strings.EqualFold(string(s), "glutes") {
				found = true
				break
			}
		}
		assert.True(t, found || strings.EqualFold(string(e.PrimaryMuscleGroup), "glutes"))
	}
}

func TestSearch_MuscleGroupPrimaryOnly(t *testing.T) {
	// Hammer Curl has biceps as primary; Back Squat lists biceps in no
	// slot (only quadriceps / glutes / hamstrings). We add a synthetic
	// entry with biceps as *secondary* to prove primary_only filters it out.
	entries := fixture()
	entries = append(entries, hevy.ExerciseTemplate{
		ID: "ZZZZZZ", Title: "Pretend Row (Barbell)", Type: "weight_reps",
		PrimaryMuscleGroup:    "upper_back",
		SecondaryMuscleGroups: []hevy.MuscleGroup{"biceps"},
	})
	withFixture(t, entries)

	full := Search(SearchParams{MuscleGroup: "biceps", Limit: 50})
	primary := Search(SearchParams{MuscleGroup: "biceps", MuscleGroupPrimaryOnly: true, Limit: 50})

	// full includes the secondary-biceps row; primary-only does not.
	assert.Greater(t, len(full), len(primary))
	for _, e := range primary {
		assert.Equal(t, "biceps", string(e.PrimaryMuscleGroup),
			"primary_only must exclude %s (primary=%s)", e.Title, e.PrimaryMuscleGroup)
	}
	// Sanity: the secondary-only entry should be in full but not primary.
	hasSecondary := func(list []hevy.ExerciseTemplate) bool {
		for _, e := range list {
			if e.ID == "ZZZZZZ" {
				return true
			}
		}
		return false
	}
	assert.True(t, hasSecondary(full), "full match should include the secondary-biceps row")
	assert.False(t, hasSecondary(primary), "primary-only must exclude the secondary-biceps row")
}

func TestSearch_FiltersCombineWithAnd(t *testing.T) {
	withFixture(t, fixture())
	got := Search(SearchParams{Query: "curl", MuscleGroup: "biceps", Limit: 10})
	require.Len(t, got, 2)
	for _, e := range got {
		assert.Contains(t, strings.ToLower(e.Title), "curl")
	}
}

func TestSearch_LimitIsRespected(t *testing.T) {
	withFixture(t, fixture())
	got := Search(SearchParams{Limit: 3})
	assert.Len(t, got, 3)
}

func TestSearch_UnknownMuscleGroupReturnsEmpty(t *testing.T) {
	withFixture(t, fixture())
	got := Search(SearchParams{MuscleGroup: "no_such_muscle", Limit: 10})
	assert.Empty(t, got)
}

func TestSearch_ExerciseTypeFilter(t *testing.T) {
	withFixture(t, fixture())
	got := Search(SearchParams{ExerciseType: "distance_duration", Limit: 10})
	require.Len(t, got, 1)
	assert.Equal(t, "Rowing Machine", got[0].Title)
}

func TestSearch_EmptyCatalogReturnsEmpty(t *testing.T) {
	withFixture(t, nil)
	got := Search(SearchParams{Query: "anything", Limit: 10})
	assert.Empty(t, got)
}

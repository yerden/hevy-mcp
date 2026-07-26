// Command gen refreshes the embedded public exercise-template catalog by
// paging through Hevy's /v1/exercise_templates endpoint. It writes the result
// to ../catalog.json (relative to this file), sorted by template ID for stable
// diffs.
//
// Invoked via `go generate ./internal/exercises/...` — requires HEVY_API_KEY
// in the environment (any valid key works; only the public catalog is kept).
package main

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"time"

	"github.com/yerden/hevy-mcp/internal/hevy"
)

const outputPath = "catalog.json"

func main() {
	apiKey := os.Getenv("HEVY_API_KEY")
	if apiKey == "" {
		log.Fatal("HEVY_API_KEY is required")
	}

	client := hevy.New(apiKey, "")
	all := []hevy.ExerciseTemplate{}

	page := 1
	const pageSize = 10
	for {
		log.Printf("fetching page %d", page)
		resp, err := client.ListExerciseTemplates(page, pageSize)
		if err != nil {
			log.Fatalf("list page %d: %v", page, err)
		}
		for _, t := range resp.ExerciseTemplates {
			if t.IsCustom {
				continue
			}
			all = append(all, t)
		}
		if page >= resp.PageCount || len(resp.ExerciseTemplates) == 0 {
			break
		}
		page++
		time.Sleep(50 * time.Millisecond) // gentle on Hevy
	}

	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })

	buf, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}
	buf = append(buf, '\n')
	if err := os.WriteFile(outputPath, buf, 0644); err != nil {
		log.Fatalf("write %s: %v", outputPath, err)
	}
	log.Printf("wrote %d public exercise templates to %s", len(all), outputPath)
}

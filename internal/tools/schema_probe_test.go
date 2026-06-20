package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/yerden/hevy-mcp/internal/hevy"
)

type probeArgs struct {
	Routine hevy.Routine `json:"routine"`
}

// Schema probe: prove that WithInputSchema[T] emits a schema that mentions
// folder_id (which is what the LLM needs to see in order to populate it).
func TestSchemaProbe_RoutineMentionsFolderID(t *testing.T) {
	tool := mcp.NewTool("probe", mcp.WithInputSchema[probeArgs]())
	out, err := json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "folder_id") {
		t.Logf("schema:\n%s", s)
		t.Fatalf("schema does not mention folder_id")
	}
	if !strings.Contains(s, "exercise_template_id") {
		t.Logf("schema:\n%s", s)
		t.Fatalf("schema does not mention exercise_template_id")
	}
	t.Logf("schema:\n%s", s)
}

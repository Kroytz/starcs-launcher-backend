package mysqlrepo

import (
	"encoding/json"
	"testing"
)

func TestTaskCriteriaMatchesSubset(t *testing.T) {
	criteria := json.RawMessage(`{"mode":"ZM","won":true,"details":{"team":"human"}}`)
	dimensions := json.RawMessage(`{"mode":"ZM","won":true,"map":"zm_example","details":{"team":"human","score":5}}`)
	matched, err := taskCriteriaMatches(criteria, dimensions)
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("expected criteria subset to match dimensions")
	}

	matched, err = taskCriteriaMatches(json.RawMessage(`{"mode":"ZE"}`), dimensions)
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("different mode must not match")
	}
}

func TestEnrichTaskEventDimensionsPreservesPayload(t *testing.T) {
	encoded, err := enrichTaskEventDimensions(json.RawMessage(`{"mode":"ZM","won":true}`), "zm-01", "ZombieZeta")
	if err != nil {
		t.Fatal(err)
	}
	var dimensions map[string]any
	if err := json.Unmarshal(encoded, &dimensions); err != nil {
		t.Fatal(err)
	}
	if dimensions["serverId"] != "zm-01" || dimensions["source"] != "ZombieZeta" || dimensions["mode"] != "ZM" {
		t.Fatalf("unexpected enriched dimensions: %#v", dimensions)
	}
}

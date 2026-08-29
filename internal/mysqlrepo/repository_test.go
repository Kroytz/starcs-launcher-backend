package mysqlrepo

import "testing"

func TestAnnouncementSummaryUsesFirstTextBlock(t *testing.T) {
	raw := `{"sections":[{"title":"更新说明","blocks":[{"kind":"paragraph","text":"第一段公告内容"}]}]}`
	if got := announcementSummary(raw); got != "第一段公告内容" {
		t.Fatalf("unexpected summary %q", got)
	}
}

func TestCollectionCountSupportsJSONAndDelimitedText(t *testing.T) {
	tests := map[string]int{
		`[]`:          0,
		`[1,2,3]`:     3,
		`{"a":1}`:     1,
		`ZM,ZE,TTT`:   3,
		`reward#next`: 2,
	}
	for input, expected := range tests {
		if got := collectionCount(input); got != expected {
			t.Errorf("collectionCount(%q)=%d, expected %d", input, got, expected)
		}
	}
}

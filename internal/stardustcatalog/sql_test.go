package stardustcatalog

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteImportSQLUsesHexAndUpsert(t *testing.T) {
	var output bytes.Buffer
	err := WriteImportSQL(&output, []Item{{
		Type: "chattag", UniqueID: "quote'test", Group: "个性化", Category: "头衔",
		DisplayName: "测试'称号", Price: 1288, Slot: 1, Purchasable: true,
		ConfigJSON: []byte(`{"value":"测试'称号"}`), Sort: 1,
	}})
	if err != nil {
		t.Fatalf("write import SQL: %v", err)
	}
	sql := output.String()
	if strings.Contains(sql, "测试") || strings.Contains(sql, "quote'test") {
		t.Fatal("user-controlled text should be emitted as UTF-8 hex")
	}
	for _, expected := range []string{"CREATE TABLE IF NOT EXISTS", "START TRANSACTION", "ON DUPLICATE KEY UPDATE", "COMMIT;"} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("generated SQL is missing %q", expected)
		}
	}
}

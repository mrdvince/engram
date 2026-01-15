package main

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
)

func TestValidateSQL_DangerousOps(t *testing.T) {
	tests := []struct {
		name       string
		sql        string
		allowWrite bool
		wantErr    bool
	}{
		{"drop table blocked", "DROP TABLE entities", false, true},
		{"drop with leading space blocked", "  DROP TABLE entities", false, true},
		{"truncate blocked", "TRUNCATE TABLE entities", false, true},
		{"alter blocked", "ALTER TABLE entities ADD COLUMN foo TEXT", false, true},
		{"create blocked", "CREATE TABLE foo (id INT)", false, true},
		{"drop in content allowed", "INSERT INTO observations (entity_id, content) VALUES (1, 'DROP this')", true, false},
		{"alter in content allowed", "INSERT INTO observations (entity_id, content) VALUES (1, 'ALTER that')", true, false},
		{"create in content allowed", "INSERT INTO observations (entity_id, content) VALUES (1, 'CREATE new')", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSQL(tt.sql, tt.allowWrite)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSQL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSQL_ToolSeparation(t *testing.T) {
	tests := []struct {
		name       string
		sql        string
		allowWrite bool
		wantErr    bool
	}{
		{"select in query allowed", "SELECT * FROM entities", false, false},
		{"select in execute blocked", "SELECT * FROM entities", true, true},
		{"insert in execute allowed", "INSERT INTO entities (name, entity_type) VALUES ('test', 'Test')", true, false},
		{"insert in query blocked", "INSERT INTO entities (name, entity_type) VALUES ('test', 'Test')", false, true},
		{"update in execute allowed", "UPDATE entities SET name = 'foo' WHERE id = 1", true, false},
		{"update in query blocked", "UPDATE entities SET name = 'foo' WHERE id = 1", false, true},
		{"delete in execute allowed", "DELETE FROM entities WHERE id = 1", true, false},
		{"delete in query blocked", "DELETE FROM entities WHERE id = 1", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSQL(tt.sql, tt.allowWrite)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSQL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSQL_CaseInsensitive(t *testing.T) {
	tests := []struct {
		name       string
		sql        string
		allowWrite bool
		wantErr    bool
	}{
		{"lowercase select", "select * from entities", false, false},
		{"mixed case select", "SeLeCt * FROM entities", false, false},
		{"lowercase drop blocked", "drop table entities", false, true},
		{"mixed case drop blocked", "DrOp TaBlE entities", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSQL(tt.sql, tt.allowWrite)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSQL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestObservationInsertDetection(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		isMatch bool
	}{
		{"basic insert", "INSERT INTO observations (entity_id, content) VALUES (1, 'test')", true},
		{"lowercase", "insert into observations (entity_id, content) values (1, 'test')", true},
		{"mixed case", "Insert Into Observations (entity_id, content) VALUES (1, 'test')", true},
		{"with leading space", "  INSERT INTO observations (entity_id, content) VALUES (1, 'test')", true},
		{"with newline", "\nINSERT INTO observations (entity_id, content) VALUES (1, 'test')", true},
		{"insert into entities", "INSERT INTO entities (name, entity_type) VALUES ('test', 'Test')", false},
		{"insert into relations", "INSERT INTO relations (from_id, to_id, relation_type) VALUES (1, 2, 'test')", false},
		{"insert into tags", "INSERT INTO tags (name, description) VALUES ('test', 'desc')", false},
		{"select from observations", "SELECT * FROM observations", false},
		{"observations in content", "INSERT INTO entities (name, entity_type) VALUES ('observations test', 'Test')", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := observationInsert.MatchString(tt.sql)
			if got != tt.isMatch {
				t.Errorf("observationInsert.MatchString(%q) = %v, want %v", tt.sql, got, tt.isMatch)
			}
		})
	}
}

func TestParseTagNames(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"single tag", "homelab", []string{"homelab"}},
		{"multiple tags", "homelab,career", []string{"homelab", "career"}},
		{"with spaces", " homelab , career , personal ", []string{"homelab", "career", "personal"}},
		{"empty string", "", nil},
		{"only commas", ",,", nil},
		{"trailing comma", "homelab,", []string{"homelab"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTagNames(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("parseTagNames(%q) = %v, want %v", tt.input, got, tt.expected)
				return
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("parseTagNames(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("LIBSQL_URL")
	if url == "" {
		url = "http://localhost:8080"
	}
	db, err := sql.Open("libsql", url)
	if err != nil {
		t.Skipf("skipping integration test: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("skipping integration test: %v", err)
	}
	return db
}

func callQuery(db *sql.DB, sqlStr string) (*mcp.CallToolResult, error) {
	handler := queryHandler(db)
	req := mcp.CallToolRequest{}
	req.Params.Name = "query"
	req.Params.Arguments = map[string]any{"sql": sqlStr}
	return handler(context.Background(), req)
}

func callExecute(db *sql.DB, sqlStr string) (*mcp.CallToolResult, error) {
	return callExecuteWithTags(db, sqlStr, "")
}

func callExecuteWithTags(db *sql.DB, sqlStr string, tags string) (*mcp.CallToolResult, error) {
	handler := executeHandler(db)
	req := mcp.CallToolRequest{}
	req.Params.Name = "execute"
	args := map[string]any{"sql": sqlStr}
	if tags != "" {
		args["tags"] = tags
	}
	req.Params.Arguments = args
	return handler(context.Background(), req)
}

func TestQueryHandler_Integration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	t.Run("select returns rows", func(t *testing.T) {
		result, err := callQuery(db, "SELECT name FROM entities LIMIT 1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("got error result: %v", result.Content)
		}
	})

	t.Run("drop blocked", func(t *testing.T) {
		result, err := callQuery(db, "DROP TABLE entities")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error for DROP")
		}
	})

	t.Run("insert blocked in query", func(t *testing.T) {
		result, err := callQuery(db, "INSERT INTO entities (name, entity_type) VALUES ('x', 'y')")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error for INSERT in query tool")
		}
	})
}

func TestExecuteHandler_Integration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	t.Run("insert and delete", func(t *testing.T) {
		result, err := callExecute(db, "INSERT INTO entities (name, entity_type) VALUES ('test_entry_12345', 'Test')")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("insert failed: %v", result.Content)
		}

		result, err = callExecute(db, "DELETE FROM entities WHERE name = 'test_entry_12345'")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("delete failed: %v", result.Content)
		}
	})

	t.Run("select blocked in execute", func(t *testing.T) {
		result, err := callExecute(db, "SELECT * FROM entities")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error for SELECT in execute tool")
		}
	})

	t.Run("drop blocked", func(t *testing.T) {
		result, err := callExecute(db, "DROP TABLE entities")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error for DROP")
		}
	})

	t.Run("content with DROP allowed", func(t *testing.T) {
		result, err := callExecute(db, "INSERT INTO entities (name, entity_type) VALUES ('DROP test 67890', 'Test')")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("insert with DROP in content should work: %v", result.Content)
		}

		callExecute(db, "DELETE FROM entities WHERE name = 'DROP test 67890'")
	})
}

func TestObservationTagsRequired_Integration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	t.Run("observation insert without tags fails", func(t *testing.T) {
		result, err := callExecute(db, "INSERT INTO observations (entity_id, content) VALUES (1, 'test observation')")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error when inserting observation without tags")
		}
	})

	t.Run("observation insert with invalid tag fails", func(t *testing.T) {
		result, err := callExecuteWithTags(db, "INSERT INTO observations (entity_id, content) VALUES (1, 'test observation')", "nonexistent_tag_xyz")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error when inserting observation with invalid tag")
		}
	})

	t.Run("observation insert with valid tag succeeds", func(t *testing.T) {
		result, err := callExecuteWithTags(db, "INSERT INTO observations (entity_id, content) VALUES (1, 'test observation for tags 98765')", "homelab")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("observation insert with valid tag should work: %v", result.Content)
		}

		callExecute(db, "DELETE FROM observations WHERE content = 'test observation for tags 98765'")
	})

	t.Run("observation insert with multiple valid tags succeeds", func(t *testing.T) {
		result, err := callExecuteWithTags(db, "INSERT INTO observations (entity_id, content) VALUES (1, 'test observation multi tags 54321')", "homelab,career")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("observation insert with multiple tags should work: %v", result.Content)
		}

		callExecute(db, "DELETE FROM observations WHERE content = 'test observation multi tags 54321'")
	})

	t.Run("entity insert still works without tags", func(t *testing.T) {
		result, err := callExecute(db, "INSERT INTO entities (name, entity_type) VALUES ('tag_test_entity_11111', 'Test')")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("entity insert should not require tags: %v", result.Content)
		}

		callExecute(db, "DELETE FROM entities WHERE name = 'tag_test_entity_11111'")
	})
}

package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
)

var (
	dbURL             = getEnv("LIBSQL_URL", "http://localhost:8080")
	dangerousOps      = regexp.MustCompile(`(?i)^\s*(DROP|TRUNCATE|ALTER|CREATE|ATTACH|DETACH)\b`)
	writeOps          = regexp.MustCompile(`(?i)^\s*(INSERT|UPDATE|DELETE)\b`)
	observationInsert = regexp.MustCompile(`(?i)^\s*INSERT\s+INTO\s+observations\b`)
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	db, err := sql.Open("libsql", dbURL)
	if err != nil {
		log.Fatalf("failed to connect to libsql: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("failed to ping libsql: %v", err)
	}

	s := server.NewMCPServer(
		"memory-mcp",
		"1.0.0",
		server.WithResourceCapabilities(true, false),
		server.WithLogging(),
	)

	s.AddResource(mcp.NewResource(
		"memory://schema",
		"Database schema",
		mcp.WithResourceDescription("Table definitions for the memory database"),
		mcp.WithMIMEType("text/plain"),
	), schemaHandler())

	s.AddTool(mcp.NewTool("query",
		mcp.WithDescription(`Execute a SELECT query and return results.

All observations are tagged with broad categories. Check tags first to find what you're looking for:
  SELECT name, description FROM tags

Then filter observations by tag via observation_tags junction table. Build whatever query you need from there.`),
		mcp.WithString("sql",
			mcp.Required(),
			mcp.Description("SQL SELECT statement to execute"),
		),
	), queryHandler(db))

	s.AddTool(mcp.NewTool("execute",
		mcp.WithDescription(`Execute INSERT, UPDATE, or DELETE statement. Use this for writing data.

IMPORTANT: When inserting observations, you MUST provide the tags parameter.
Tags are broad categories: homelab, career, drinks, personal.
Query 'SELECT name, description FROM tags' to see available tags.
If you need a new tag, ask the user first before creating it.`),
		mcp.WithString("sql",
			mcp.Required(),
			mcp.Description("SQL statement (INSERT, UPDATE, or DELETE)"),
		),
		mcp.WithString("tags",
			mcp.Description("Required for observation inserts. Comma-separated tag names, e.g. 'homelab' or 'career,personal'"),
		),
	), executeHandler(db))

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func schemaHandler() server.ResourceHandlerFunc {
	schema := `-- memory database schema

entities (id, name, entity_type, created_at)
observations (id, entity_id, content, created_at)
relations (id, from_id, to_id, relation_type, created_at)
tags (id, name, description, created_at)
observation_tags (observation_id, tag_id)

All observations are categorized via tags. Query tags first to see available categories:
  SELECT name, description FROM tags

When inserting observations, the 'tags' parameter is required in execute tool.
`
	return func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "memory://schema",
				MIMEType: "text/plain",
				Text:     schema,
			},
		}, nil
	}
}

func validateSQL(sql string, allowWrite bool) error {
	if dangerousOps.MatchString(sql) {
		return fmt.Errorf("dangerous operation not allowed: DROP, TRUNCATE, ALTER, CREATE, ATTACH, DETACH are blocked")
	}

	isWrite := writeOps.MatchString(sql)
	if isWrite && !allowWrite {
		return fmt.Errorf("write operations not allowed in query tool, use execute tool instead")
	}
	if !isWrite && allowWrite {
		return fmt.Errorf("SELECT not allowed in execute tool, use query tool instead")
	}

	return nil
}

func queryHandler(db *sql.DB) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sqlStr := request.GetString("sql", "")
		if strings.TrimSpace(sqlStr) == "" {
			return mcp.NewToolResultError("sql parameter is required"), nil
		}

		if err := validateSQL(sqlStr, false); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		rows, err := db.QueryContext(ctx, sqlStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query error: %v", err)), nil
		}
		defer rows.Close()

		cols, err := rows.Columns()
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("columns error: %v", err)), nil
		}

		var results []map[string]any
		for rows.Next() {
			values := make([]any, len(cols))
			pointers := make([]any, len(cols))
			for i := range values {
				pointers[i] = &values[i]
			}

			if err := rows.Scan(pointers...); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("scan error: %v", err)), nil
			}

			row := make(map[string]any)
			for i, col := range cols {
				row[col] = values[i]
			}
			results = append(results, row)
		}

		if len(results) == 0 {
			return mcp.NewToolResultText("no results"), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("rows: %d\n\n", len(results)))

		for i, row := range results {
			sb.WriteString(fmt.Sprintf("--- row %d ---\n", i+1))
			for _, col := range cols {
				sb.WriteString(fmt.Sprintf("%s: %v\n", col, row[col]))
			}
			sb.WriteString("\n")
		}

		return mcp.NewToolResultText(sb.String()), nil
	}
}

func executeHandler(db *sql.DB) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sqlStr := request.GetString("sql", "")
		if strings.TrimSpace(sqlStr) == "" {
			return mcp.NewToolResultError("sql parameter is required"), nil
		}

		if err := validateSQL(sqlStr, true); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		tagsStr := request.GetString("tags", "")
		isObservationInsert := observationInsert.MatchString(sqlStr)

		if isObservationInsert {
			if strings.TrimSpace(tagsStr) == "" {
				return mcp.NewToolResultError("tags parameter is required when inserting observations. Use broad categories like: homelab, career, drinks, personal. Query 'SELECT name, description FROM tags' to see all available tags."), nil
			}

			tagNames := parseTagNames(tagsStr)
			tagIDs, err := validateTags(ctx, db, tagNames)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			result, err := db.ExecContext(ctx, sqlStr)
			if err != nil {
				return mcp.NewToolResultError(formatExecError(err)), nil
			}

			observationID, _ := result.LastInsertId()
			if observationID > 0 {
				if err := linkTags(ctx, db, observationID, tagIDs); err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("observation created but failed to link tags: %v", err)), nil
				}
			}

			return mcp.NewToolResultText(fmt.Sprintf("success: observation %d created with tags: %s", observationID, tagsStr)), nil
		}

		result, err := db.ExecContext(ctx, sqlStr)
		if err != nil {
			return mcp.NewToolResultError(formatExecError(err)), nil
		}

		affected, _ := result.RowsAffected()
		lastID, _ := result.LastInsertId()

		if lastID > 0 {
			return mcp.NewToolResultText(fmt.Sprintf("success: %d row(s) affected, last insert id: %d", affected, lastID)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("success: %d row(s) affected", affected)), nil
	}
}

func parseTagNames(tagsStr string) []string {
	var tags []string
	for _, t := range strings.Split(tagsStr, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

func validateTags(ctx context.Context, db *sql.DB, tagNames []string) ([]int64, error) {
	var tagIDs []int64
	var missing []string

	for _, name := range tagNames {
		var id int64
		err := db.QueryRowContext(ctx, "SELECT id FROM tags WHERE name = ?", name).Scan(&id)
		if err == sql.ErrNoRows {
			missing = append(missing, name)
		} else if err != nil {
			return nil, fmt.Errorf("error checking tag '%s': %v", name, err)
		} else {
			tagIDs = append(tagIDs, id)
		}
	}

	if len(missing) > 0 {
		rows, err := db.QueryContext(ctx, "SELECT name, description FROM tags ORDER BY name")
		if err != nil {
			return nil, fmt.Errorf("unknown tag(s): %s", strings.Join(missing, ", "))
		}
		defer rows.Close()

		var available []string
		for rows.Next() {
			var name, desc string
			rows.Scan(&name, &desc)
			available = append(available, fmt.Sprintf("%s (%s)", name, desc))
		}

		return nil, fmt.Errorf("unknown tag(s): %s\n\nAvailable tags:\n%s\n\nIf you need a new tag, ask the user first before creating it with: INSERT INTO tags (name, description) VALUES ('name', 'description')",
			strings.Join(missing, ", "), strings.Join(available, "\n"))
	}

	return tagIDs, nil
}

func linkTags(ctx context.Context, db *sql.DB, observationID int64, tagIDs []int64) error {
	for _, tagID := range tagIDs {
		_, err := db.ExecContext(ctx, "INSERT INTO observation_tags (observation_id, tag_id) VALUES (?, ?)", observationID, tagID)
		if err != nil {
			return err
		}
	}
	return nil
}

func formatExecError(err error) string {
	errMsg := err.Error()
	if strings.Contains(errMsg, "UNIQUE constraint") {
		return fmt.Sprintf("duplicate entry: %v", err)
	}
	if strings.Contains(errMsg, "FOREIGN KEY constraint") {
		return fmt.Sprintf("referenced entity does not exist: %v", err)
	}
	if strings.Contains(errMsg, "CHECK constraint") {
		return fmt.Sprintf("validation failed (empty or invalid value): %v", err)
	}
	return fmt.Sprintf("execute error: %v", err)
}

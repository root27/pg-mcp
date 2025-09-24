package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	_ "github.com/lib/pq"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
)

type PostgresServer struct {
	db *sql.DB
}

// DatabaseConfig holds the database connection configuration
type DatabaseConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
	SSLMode  string `json:"sslmode"`
}

// QueryResult represents the result of a database query
type QueryResult struct {
	Columns []string                 `json:"columns"`
	Rows    []map[string]interface{} `json:"rows"`
	Count   int                      `json:"count"`
}

func NewPostgresServer(config DatabaseConfig) (*PostgresServer, error) {
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Host, config.Port, config.User, config.Password, config.DBName, config.SSLMode)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &PostgresServer{db: db}, nil
}

// Close closes the database connection
func (s *PostgresServer) Close() error {
	return s.db.Close()
}

func (s *PostgresServer) isSafeQuery(query string) error {
	query = strings.TrimSpace(strings.ToLower(query))

	// Block dangerous operations
	dangerousPatterns := []string{
		`\bdrop\s+table\b`,
		`\bdrop\s+database\b`,
		`\bdrop\s+schema\b`,
		`\btruncate\b`,
		`\bdelete\s+from\b`,
		`\bupdate\s+.*\s+set\b`,
		`\binsert\s+into\b`,
		`\balter\s+table\b`,
		`\bcreate\s+table\b`,
		`\bgrant\b`,
		`\brevoke\b`,
	}

	for _, pattern := range dangerousPatterns {
		matched, err := regexp.MatchString(pattern, query)
		if err != nil {
			return fmt.Errorf("regex error: %w", err)
		}
		if matched {
			return fmt.Errorf("query contains potentially dangerous operation: %s", pattern)
		}
	}

	if !strings.HasPrefix(query, "select") && !strings.HasPrefix(query, "with") {
		return fmt.Errorf("only SELECT and CTE (WITH) queries are allowed")
	}

	return nil
}

func (s *PostgresServer) setupMCPTools(mcpServer *server.MCPServer) {

	queryTool := mcp.NewTool(
		"postgres_query",
		mcp.WithDescription("Execute a SQL query against the PostgreSQL database"),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("The SQL query to execute (only SELECT and CTE queries are allowed)"),
		),
	)

	listTablesTool := mcp.NewTool(
		"list_tables",
		mcp.WithDescription("List all tables in the PostgreSQL database"),
	)

	describeTableTool := mcp.NewTool(
		"describe_table",
		mcp.WithDescription("Describe the columns of a specified table"),
		mcp.WithString("table",
			mcp.Required(),
			mcp.Description("Name of the table to describe"),
		),
	)

	mcpServer.AddTool(queryTool, s.ExecuteQuery)
	mcpServer.AddTool(listTablesTool, s.ListTables)
	mcpServer.AddTool(describeTableTool, s.DescribeTable)
}

func (s *PostgresServer) ListTables(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT table_name 
        FROM information_schema.tables 
        WHERE table_schema = 'public'
    `)
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}

	response, _ := json.Marshal(tables)
	return mcp.NewToolResultText(string(response)), nil
}

func (s *PostgresServer) DescribeTable(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	table, err := req.RequireString("table")
	if err != nil {
		return mcp.NewToolResultError("Missing required parameter 'table'"), nil
	}

	rows, err := s.db.QueryContext(ctx, `
        SELECT column_name, data_type
        FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = $1
        ORDER BY ordinal_position
    `, table)
	if err != nil {
		return nil, fmt.Errorf("failed to describe table: %w", err)
	}
	defer rows.Close()

	var columns []map[string]string
	for rows.Next() {
		var name, dtype string
		if err := rows.Scan(&name, &dtype); err != nil {
			return nil, err
		}
		columns = append(columns, map[string]string{"column": name, "type": dtype})
	}

	response, _ := json.Marshal(columns)
	return mcp.NewToolResultText(string(response)), nil
}

func (s *PostgresServer) ExecuteQuery(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("Missing required parameter 'query'"), nil
	}

	if err := s.isSafeQuery(query); err != nil {
		return nil, fmt.Errorf("unsafe query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		if strings.Contains(err.Error(), "column") || strings.Contains(err.Error(), "table") {
			schemaInfo, schemaErr := s.getSchemaInfo(ctx)
			if schemaErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Query failed: %v. Also failed to fetch schema: %v", err, schemaErr)), nil
			}

			schemaJSON, _ := json.Marshal(schemaInfo)
			return mcp.NewToolResultError(fmt.Sprintf("Query failed: %v\n\nHere is the schema:\n%s", err, schemaJSON)), nil
		}

		return mcp.NewToolResultError(fmt.Sprintf("Query failed: %v", err)), nil
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	results := make([]map[string]interface{}, 0)
	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range columns {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		rowMap := make(map[string]interface{})
		for i, colName := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				rowMap[colName] = string(b)
			} else {
				rowMap[colName] = val
			}
		}
		results = append(results, rowMap)
	}

	response := QueryResult{
		Columns: columns,
		Rows:    results,
		Count:   len(results),
	}
	responseJSON, _ := json.Marshal(response)

	return mcp.NewToolResultText(string(responseJSON)), nil
}

func (s *PostgresServer) getSchemaInfo(ctx context.Context) (map[string][]map[string]string, error) {
	schemaInfo := make(map[string][]map[string]string)

	// Get all tables
	tableRows, err := s.db.QueryContext(ctx, `
        SELECT table_name 
        FROM information_schema.tables 
        WHERE table_schema = 'public'
    `)
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}
	defer tableRows.Close()

	var tables []string
	for tableRows.Next() {
		var t string
		if err := tableRows.Scan(&t); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}

	// Get columns for each table
	for _, table := range tables {
		colRows, err := s.db.QueryContext(ctx, `
            SELECT column_name, data_type
            FROM information_schema.columns
            WHERE table_schema = 'public' AND table_name = $1
            ORDER BY ordinal_position
        `, table)
		if err != nil {
			return nil, fmt.Errorf("failed to describe table %s: %w", table, err)
		}

		var cols []map[string]string
		for colRows.Next() {
			var name, dtype string
			if err := colRows.Scan(&name, &dtype); err != nil {
				return nil, err
			}
			cols = append(cols, map[string]string{"column": name, "type": dtype})
		}
		schemaInfo[table] = cols
		colRows.Close()
	}

	return schemaInfo, nil
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, mcp-protocol-version,mcp-session-id")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})

}

func main() {

	var transport string
	flag.StringVar(&transport, "t", "stdio", "Transport type (stdio or http)")
	flag.StringVar(&transport, "transport", "stdio", "Transport type (stdio or http)")
	flag.Parse()

	// Load database configuration from environment variables
	config := DatabaseConfig{
		Host:     getEnv("DB_HOST", "localhost"),
		Port:     getEnvInt("DB_PORT", 5432),
		User:     getEnv("DB_USER", "postgres"),
		Password: getEnv("DB_PASSWORD", "password"),
		DBName:   getEnv("DB_NAME", "mydb"),
		SSLMode:  getEnv("DB_SSLMODE", "disable"),
	}

	pgServer, err := NewPostgresServer(config)
	if err != nil {
		log.Fatalf("Failed to create PostgreSQL server: %v", err)
	}
	defer pgServer.Close()

	mcpServer := server.NewMCPServer(
		"postgres-mcp-server",
		"1.0.0",
		server.WithLogging(),
	)

	pgServer.setupMCPTools(mcpServer)

	log.Println("Starting PostgreSQL MCP Server...")
	log.Printf("Connected to database: %s@%s:%d/%s", config.User, config.Host, config.Port, config.DBName)

	if transport == "http" {
		httpServer := server.NewStreamableHTTPServer(mcpServer)

		handler := corsMiddleware(httpServer)

		customServer := &http.Server{
			Addr:    ":8080",
			Handler: handler,
		}

		log.Printf("HTTP server listening on :8080/mcp")
		if err := customServer.ListenAndServe(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	} else {
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := fmt.Sscanf(value, "%d", &defaultValue); err == nil && intValue == 1 {
			return defaultValue
		}
	}
	return defaultValue
}

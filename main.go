package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	_ "github.com/lib/pq"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
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
	)

	mcpServer.AddTool(queryTool, s.ExecuteQuery)

}

func (s *PostgresServer) ExecuteQuery(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	query, err := req.RequireString("query")

	if err != nil {

		return mcp.NewToolResultError(err.Error()), nil

	}

	if err := s.isSafeQuery(query); err != nil {
		return nil, fmt.Errorf("unsafe query: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
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
	count := len(results)
	response := QueryResult{
		Columns: columns,
		Rows:    results,
		Count:   count,
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}
	return mcp.NewToolResultText(string(responseJSON)), nil

}

func main() {
	// Load database configuration from environment variables
	config := DatabaseConfig{
		Host:     getEnv("DB_HOST", "localhost"),
		Port:     getEnvInt("DB_PORT", 5432),
		User:     getEnv("DB_USER", ""),
		Password: getEnv("DB_PASSWORD", ""),
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

	// Start the server
	if err := server.ServeStdio(mcpServer); err != nil {
		log.Fatalf("Server failed: %v", err)
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

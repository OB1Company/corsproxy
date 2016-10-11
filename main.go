package main

import (
	"crypto/tls"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/gocraft/health"
	_ "github.com/mattn/go-sqlite3"
)

// nodeTableSchema is a SQL statement that creates the logging table
const nodeTableSchema = `CREATE TABLE IF NOT EXISTS nodes (
  ip TEXT NOT NULL,
  state TEXT NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  PRIMARY KEY(ip, state)
  );`

// HTTPTimeout is the amount of time to wait for a read/write timeout on the request
var HTTPTimeout = 15 * time.Second

// HTTPClient is a custom HTTP client that doesn't check tls signature chains
var HTTPClient = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
	Timeout: HTTPTimeout,
}

// stream is a health.Stream used for instrumentation
var stream *health.Stream

func main() {
	// Create health stream
	stream = health.NewStream()
	stream.AddSink(&health.WriterSink{os.Stdout})

	// Get host and port to bind to
	port := getOSEnvString("CORS_PROXY_PORT", "8080")
	host := getOSEnvString("CORS_PROXY_HOST", "127.0.0.1")
	dbFile := getOSEnvString("CORS_PROXY_DB_FILE", "/opt/corsproxy.db")

	// Open DB and create logging middleware
	db, err := openDB(dbFile)
	if err != nil {
		stream.EventErrKv("open_db", err, health.Kvs{"file": dbFile})
		return
	}

	updateNodeStateMiddleware, err := newUpdateNodeStateMiddleware(db)
	if err != nil {
		stream.EventErr("new_log_middleware", err)
		return
	}

	// Create a router to the proxy request handler
	router := newRouter(updateNodeStateMiddleware)

	// Start listening
	stream.EventKv("server_listening", health.Kvs{"host": host, "port": port})
	http.ListenAndServe(host+":"+port, router)
}

// openDB opens a sqlite connection and creates the database/schema if it
// doesn't exist yet
func openDB(dbFile string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbFile)
	if err != nil {
		return nil, err
	}
	if db == nil {
		err = errors.New("db is nil")
		return nil, err
	}

	// Create table if not exists
	_, err = db.Exec(nodeTableSchema)
	if err != nil {
		return nil, err
	}

	return db, nil
}

// getOSEnvString returns the environment variable with the given name or the
// defaultVal if no env var is set for the name
func getOSEnvString(name string, defaultVal string) string {
	val := os.Getenv(name)
	if val != "" {
		return val
	}
	return defaultVal
}

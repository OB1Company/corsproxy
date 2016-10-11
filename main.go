package main

import (
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/gocraft/health"
	"github.com/gocraft/web"
	_ "github.com/mattn/go-sqlite3"
)

// CORS headers
const accessControlAllowOriginHeader = "*"
const accessControlAllowHeadersHeader = "Origin, X-Requested-With, Content-Type, Accept"

// HTTPTimeout is the amount of time to wait for a read/write timeout on the request
var HTTPTimeout = 15 * time.Second

// HTTPClient is a custom HTTP client that doesn't check tls signature chains
var HTTPClient = &http.Client{Transport: &http.Transport{
	TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
}}

// stream is a health.Stream used for instrumentation
var stream = newStream()

// middlewareFunc is a gocraft/web compatible middleware
type middlewareFunc func(c *Context, rw web.ResponseWriter, req *web.Request, next web.NextMiddlewareFunc)

// Context is the context for incoming HTTP requests
type Context struct {
	job        *health.Job
	err        error
	nodeStatus string
	nodeIP     string
}

// StatusResponse represents the response from the ob-relay status endpoint
type StatusResponse struct {
	Status string `json:"status"`
}

// AddCORSHeaders sets the proper HTTP response headers for a CORS request
func (*Context) AddCORSHeaders(rw web.ResponseWriter, r *web.Request, next web.NextMiddlewareFunc) {
	rw.Header().Set("Access-Control-Allow-Origin", accessControlAllowOriginHeader)
	rw.Header().Set("Access-Control-Allow-Headers", accessControlAllowHeadersHeader)
	next(rw, r)
}

// HealthCheck
func (c *Context) HealthCheck(rw web.ResponseWriter, r *web.Request, next web.NextMiddlewareFunc) {
	// Setup instrumentation
	c.job = stream.NewJob(r.URL.String())

	// Execute the request
	next(rw, r)

	// We're done if no errors
	if c.err == nil {
		c.job.Complete(health.Success)
		return
	}

	// Otherwise return the errors to the caller
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(rw, `{"error":%q}`, c.err)
	c.job.Complete(health.Error)
}

// StatusRequestProxyHandler gets a status from ob-relay
func (c *Context) StatusRequestProxyHandler(rw web.ResponseWriter, r *web.Request) {
	url := "https://" + r.PathParams["ip"] + ":8080/status"

	// Perform the request
	resp, err := HTTPClient.Get(url)
	if err != nil {
		c.err = err
		c.job.EventErr("proxy.request_url", c.err)
		return
	}

	if resp.StatusCode != 200 {
		c.err = fmt.Errorf("Error in HTTP request: %d", resp.StatusCode)
		c.job.EventErr("proxy.request_url", c.err)
		return
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		c.err = err
		c.job.EventErr("proxy.read_body", c.err)
		return
	}

	status := &StatusResponse{}
	err = json.Unmarshal(body, status)
	if err != nil {
		c.err = err
		c.job.EventErr("proxy.parse_body", c.err)
		return
	}

	c.nodeStatus = status.Status
	c.nodeIP = r.PathParams["ip"]

	_, err = rw.Write(body)
	if err != nil {
		c.err = err
		c.job.EventErr("proxy.write_body", c.err)
		return
	}
}

func newUpdateNodeStateMiddleware(db *sql.DB) (middlewareFunc, error) {
	return func(c *Context, rw web.ResponseWriter, req *web.Request, next web.NextMiddlewareFunc) {
		// Execute handler
		next(rw, req)

		// Update state
		update := "INSERT OR REPLACE INTO nodes (ip, state, created_at) values(?, ?, CURRENT_TIMESTAMP)"
		stmt, err := db.Prepare(update)
		defer stmt.Close()
		if err != nil {
			c.job.EventErr("update_node_state.prepare", err)
			return
		}

		_, err = stmt.Exec(c.nodeIP, c.nodeStatus)
		if err != nil {
			c.job.EventErr("update_node_state.execute", err)
			return
		}
	}, nil
}

func main() {
	// Get host and port to bind to
	port := getOSEnvString("CORS_PROXY_PORT", "8080")
	host := getOSEnvString("CORS_PROXY_HOST", "127.0.0.1")
	dbFile := getOSEnvString("CORS_PROXY_DB_FILE", "/opt/corsproxy.db")

	// Create DB
	db, err := sql.Open("sqlite3", dbFile)
	if err != nil {
		stream.EventErrKv("open_db.connect", err, health.Kvs{"file": dbFile})
		return
	}
	if db == nil {
		err = errors.New("db is nil")
		stream.EventErrKv("open_db.connect", err, health.Kvs{"file": dbFile})
		return
	}

	// Create table if not exists
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS nodes (
		ip TEXT NOT NULL PRIMARY KEY,
		state TEXT,
		created_at DATETIME
		);`)
	if err != nil {
		stream.EventErrKv("create_table", err, health.Kvs{"file": dbFile})
		return
	}

	// Create logging middleware
	UpdateNodeStateMiddleware, err := newUpdateNodeStateMiddleware(db)
	if err != nil {
		stream.EventErr("new_log_middleware", err)
		return
	}

	// Create a router to the proxy request handler
	router := newRouter(UpdateNodeStateMiddleware)

	// Start listening
	stream.EventKv("server_listening", health.Kvs{"host": host, "port": port})
	http.ListenAndServe(host+":"+port, router)
}

func newRouter(UpdateNodeStateMiddleware middlewareFunc) *web.Router {
	return web.New(Context{}).
		Middleware((*Context).HealthCheck).
		Middleware(web.LoggerMiddleware).
		Middleware(web.ShowErrorsMiddleware).
		Middleware((*Context).AddCORSHeaders).
		Middleware(UpdateNodeStateMiddleware).
		Get("/status/:ip", (*Context).StatusRequestProxyHandler)
}

func newStream() *health.Stream {
	s := health.NewStream()
	s.AddSink(&health.WriterSink{os.Stdout})
	return s
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

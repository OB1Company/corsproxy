package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/gocraft/health"
	"github.com/gocraft/web"
)

// CORS headers
const accessControlAllowOriginHeader = "*"
const accessControlAllowHeadersHeader = "Origin, X-Requested-With, Content-Type, Accept"

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

func newRouter(UpdateNodeStateMiddleware middlewareFunc) *web.Router {
	return web.New(Context{}).
		Middleware((*Context).HealthCheck).
		Middleware(web.LoggerMiddleware).
		Middleware(web.ShowErrorsMiddleware).
		Middleware((*Context).AddCORSHeaders).
		Middleware(UpdateNodeStateMiddleware).
		Get("/status/:ip", (*Context).StatusRequestProxyHandler)
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
		updateStmt, err := db.Prepare(`
      WITH new (ip, state) AS ( VALUES('ip', 'state') )
      INSERT OR REPLACE INTO nodes (ip, state, updated_at, created_at)
      SELECT new.ip, new.state, CURRENT_TIMESTAMP, COALESCE(old.created_at, CURRENT_TIMESTAMP)
      FROM new
        LEFT JOIN nodes AS old
        ON new.ip = old.ip AND new.state = old.state
      LIMIT 1;
    `)
		defer updateStmt.Close()
		if err != nil {
			c.job.EventErr("update_node_state.prepare", err)
			return
		}

		_, err = updateStmt.Exec(c.nodeIP, c.nodeStatus)
		if err != nil {
			c.job.EventErr("update_node_state.execute", err)
			return
		}
	}, nil
}

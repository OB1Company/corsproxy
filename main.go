package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/gocraft/health"
	"github.com/gocraft/web"
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

// Context is the context for incoming HTTP requests
type Context struct {
	job *health.Job
	err error
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

// ProxyRequestHandler takes makes a request to the `url` query param and sends
// the reponse to the caller
func (c *Context) ProxyRequestHandler(rw web.ResponseWriter, r *web.Request) {
	// Make sure we have a URL to request
	url, err := url.Parse(r.PathParams["url"])
	if err != nil {
		c.err = err
		c.job.EventErr("get_url_param", c.err)
		return
	}

	// Perform the request
	url.Scheme = "https"

	// Perform the request
	resp, err := HTTPClient.Get(url.String())
	if err != nil {
		c.err = err
		c.job.EventErr("request_url", c.err)
		return
	}

	if resp.StatusCode != 200 {
		c.err = fmt.Errorf("Error in HTTP request: %d", resp.StatusCode)
		c.job.EventErr("request_url", c.err)
		return
	}

	// Copy the response to the caller
	io.Copy(rw, resp.Body)
}

func main() {
	// Get host and port to bind to
	port := getOSEnvString("CORS_PROXY_PORT", "8080")
	host := getOSEnvString("CORS_PROXY_HOST", "127.0.0.1")

	// Create a router to the proxy request handler
	router := newRouter()

	// Start listening
	stream.EventKv("server_listening", health.Kvs{"host": host, "port": port})
	http.ListenAndServe(host+":"+port, router)
}

func newRouter() *web.Router {
	return web.New(Context{}).
		Middleware((*Context).HealthCheck).
		Middleware(web.LoggerMiddleware).
		Middleware(web.ShowErrorsMiddleware).
		Middleware((*Context).AddCORSHeaders).
		Get("/:url", (*Context).ProxyRequestHandler).
		Get("/", (*Context).ProxyRequestHandler)
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

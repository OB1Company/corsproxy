package main

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jarcoal/httpmock"
)

func TestRequests(t *testing.T) {
	for _, test := range []struct {
		path       string
		statusCode int
		resultText string
	}{
		{"google.com", 200, `{"error": null}`},
		{"zoogle.com", 500, `{"error":"Error in HTTP request: 500"}`},
	} {

		// Stub requests
		httpmock.ActivateNonDefault(HTTPClient)

		httpmock.RegisterResponder("GET", "https://"+test.path,
			httpmock.NewStringResponder(test.statusCode, test.resultText))

		// Create fake request and response
		recorder := httptest.NewRecorder()
		request, err := http.NewRequest("GET", "/"+test.path, nil)
		if err != nil {
			t.Fatal(err)
		}

		// Make request
		newRouter().ServeHTTP(recorder, request)

		// Ensure we got the correct response
		body, err := ioutil.ReadAll(recorder.Body)
		if err != nil {
			t.Fatal(err)
		}

		if string(body) != test.resultText {
			t.Fatalf("Got incorrect text: %s", string(body))
		}

		if recorder.Code != test.statusCode {
			t.Fatal("Got incorrect status code: %d", recorder.Code)
		}

		// Ensure the headers were set correctly
		allowHeaders := recorder.HeaderMap["Access-Control-Allow-Headers"][0]
		if allowHeaders != accessControlAllowHeadersHeader {
			t.Fatalf("Got incorrect Access-Control-Allow-Headers: %s", allowHeaders)
		}

		originHeaders := recorder.HeaderMap["Access-Control-Allow-Origin"][0]
		if originHeaders != accessControlAllowOriginHeader {
			t.Fatalf("Got incorrect Access-Control-Allow-Origin: %s", originHeaders)
		}

		httpmock.DeactivateAndReset()
	}
}

// Package httpjson provides the bounded HTTP/JSON operations shared by the
// built-in translation providers.
package httpjson

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const (
	// MaxResponseBytes is the largest response body retained by Do.
	MaxResponseBytes = 1 << 20
	// DrainBytes bounds the additional response bytes read before closing.
	DrainBytes = 4 << 10
)

// ErrResponseTooLarge reports that a response exceeded MaxResponseBytes.
var ErrResponseTooLarge = errors.New("httpjson: response exceeds limit")

// Response contains the bounded information providers need for status and
// payload mapping.
type Response struct {
	StatusCode int
	Body       []byte
}

// DecodeJSON decodes the bounded response body into destination.
func (response Response) DecodeJSON(destination any) error {
	return json.Unmarshal(response.Body, destination)
}

// NewClient returns an HTTP client with its own clone of the default transport.
// It does not install a timeout because request contexts own all deadlines.
func NewClient() *http.Client {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		return &http.Client{Transport: transport.Clone()}
	}
	return &http.Client{Transport: http.DefaultTransport}
}

// Do encodes payload as JSON, sends a context-bound request, and captures at
// most MaxResponseBytes of the response. The response is returned alongside a
// size error so callers can retain the status code while mapping the failure.
func Do(ctx context.Context, client *http.Client, method, endpoint string, payload any) (Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, err
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	if client == nil {
		client = NewClient()
	}

	httpResponse, err := client.Do(request)
	if err != nil {
		if httpResponse != nil && httpResponse.Body != nil {
			drainAndClose(httpResponse.Body)
		}
		return Response{}, err
	}
	if httpResponse.Body == nil {
		return Response{StatusCode: httpResponse.StatusCode}, errors.New("httpjson: response has no body")
	}
	defer drainAndClose(httpResponse.Body)

	bounded, err := io.ReadAll(io.LimitReader(httpResponse.Body, MaxResponseBytes+1))
	response := Response{StatusCode: httpResponse.StatusCode, Body: bounded}
	tooLarge := len(bounded) > MaxResponseBytes
	if tooLarge {
		response.Body = bounded[:MaxResponseBytes]
	}
	if err != nil {
		return response, err
	}
	if tooLarge {
		return response, ErrResponseTooLarge
	}
	return response, nil
}

func drainAndClose(body io.ReadCloser) {
	_, _ = io.CopyN(io.Discard, body, DrainBytes)
	_ = body.Close()
}

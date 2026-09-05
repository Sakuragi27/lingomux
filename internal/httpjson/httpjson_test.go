package httpjson

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDoEncodesJSONAndCapturesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", request.Method)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		if got := request.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if got, want := string(body), `{"text":"hello"}`; got != want {
			t.Errorf("body = %q, want %q", got, want)
		}
		writer.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(writer, `{"value":"bonjour"}`)
	}))
	defer server.Close()

	response, err := Do(context.Background(), server.Client(), http.MethodPost, server.URL, struct {
		Text string `json:"text"`
	}{Text: "hello"})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	if got, want := string(response.Body), `{"value":"bonjour"}`; got != want {
		t.Errorf("response body = %q, want %q", got, want)
	}

	var decoded struct {
		Value string `json:"value"`
	}
	if err := response.DecodeJSON(&decoded); err != nil {
		t.Fatalf("DecodeJSON() error = %v", err)
	}
	if decoded.Value != "bonjour" {
		t.Errorf("decoded value = %q, want bonjour", decoded.Value)
	}
}

func TestResponseDecodeJSONRejectsMalformedJSON(t *testing.T) {
	response := Response{StatusCode: http.StatusOK, Body: []byte(`{"value":`)}
	var decoded map[string]string
	if err := response.DecodeJSON(&decoded); err == nil {
		t.Fatal("DecodeJSON() error = nil, want malformed JSON error")
	}
}

func TestDoRejectsResponsesOverOneMiBAndBoundsReads(t *testing.T) {
	body := &countingReadCloser{reader: strings.NewReader(strings.Repeat("x", MaxResponseBytes+8192))}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
		}, nil
	})}

	response, err := Do(context.Background(), client, http.MethodPost, "https://example.test/translate", struct{}{})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("Do() error = %v, want ErrResponseTooLarge", err)
	}
	if len(response.Body) != MaxResponseBytes {
		t.Errorf("body length = %d, want %d", len(response.Body), MaxResponseBytes)
	}
	if got, max := body.bytesRead.Load(), int64(MaxResponseBytes+1+DrainBytes); got > max {
		t.Errorf("read %d bytes, want at most %d", got, max)
	}
	if !body.closed.Load() {
		t.Error("response body was not closed")
	}
}

func TestDoClampsResponseBodyWhenReaderReturnsDataAndErrorAtLimit(t *testing.T) {
	readFailure := errors.New("body read failed")
	body := &dataErrorReadCloser{remaining: MaxResponseBytes + 1, finalError: readFailure}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
		}, nil
	})}

	response, err := Do(context.Background(), client, http.MethodPost, "https://example.test/translate", struct{}{})
	if !errors.Is(err, readFailure) {
		t.Fatalf("Do() error = %v, want body read failure", err)
	}
	if len(response.Body) != MaxResponseBytes {
		t.Errorf("body length = %d, want %d", len(response.Body), MaxResponseBytes)
	}
	if !body.closed.Load() {
		t.Error("response body was not closed")
	}
}

func TestDoHonorsContextCancellation(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Do(ctx, client, http.MethodPost, "https://example.test/translate", struct{}{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do() error = %v, want context.Canceled", err)
	}
}

func TestDoUsesCustomClientTransport(t *testing.T) {
	var calls atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.URL.Host != "custom.test" {
			t.Errorf("request host = %q, want custom.test", request.URL.Host)
		}
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	})}

	response, err := Do(context.Background(), client, http.MethodPost, "https://custom.test/path", struct{}{})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if response.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	if calls.Load() != 1 {
		t.Errorf("transport calls = %d, want 1", calls.Load())
	}
}

func TestNewClientClonesDefaultTransport(t *testing.T) {
	client := NewClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("default transport type = %T, want *http.Transport", http.DefaultTransport)
	}
	if transport == defaultTransport {
		t.Fatal("NewClient reused http.DefaultTransport instead of cloning it")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type countingReadCloser struct {
	reader    io.Reader
	bytesRead atomic.Int64
	closed    atomic.Bool
}

func (body *countingReadCloser) Read(buffer []byte) (int, error) {
	read, err := body.reader.Read(buffer)
	body.bytesRead.Add(int64(read))
	return read, err
}

func (body *countingReadCloser) Close() error {
	body.closed.Store(true)
	return nil
}

type dataErrorReadCloser struct {
	remaining  int
	finalError error
	closed     atomic.Bool
}

func (body *dataErrorReadCloser) Read(buffer []byte) (int, error) {
	if body.remaining == 0 {
		return 0, body.finalError
	}
	read := len(buffer)
	if read > body.remaining {
		read = body.remaining
	}
	for index := 0; index < read; index++ {
		buffer[index] = 'x'
	}
	body.remaining -= read
	if body.remaining == 0 {
		return read, body.finalError
	}
	return read, nil
}

func (body *dataErrorReadCloser) Close() error {
	body.closed.Store(true)
	return nil
}

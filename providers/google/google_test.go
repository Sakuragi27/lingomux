package google

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Sakuragi27/lingomux"
)

func TestNewValidatesConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{name: "missing API key", config: Config{}},
		{name: "whitespace API key", config: Config{APIKey: " \t"}},
		{name: "relative base URL", config: Config{APIKey: "key", BaseURL: "/relative"}},
		{name: "unsupported URL scheme", config: Config{APIKey: "key", BaseURL: "ftp://example.test"}},
		{name: "URL query", config: Config{APIKey: "key", BaseURL: "https://example.test?x=1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.config)
			if !lingomux.IsKind(err, lingomux.ErrorInvalidRequest) {
				t.Fatalf("New() error = %v, want invalid_request", err)
			}
		})
	}
}

func TestProviderNameAndInterface(t *testing.T) {
	provider, err := New(Config{APIKey: "key"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var _ lingomux.Provider = provider
	if got := provider.Name(); got != "google" {
		t.Errorf("Name() = %q, want google", got)
	}
}

func TestSupportsUsesExplicitLanguageMaps(t *testing.T) {
	provider, err := New(Config{APIKey: "key"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []struct {
		name   string
		source string
		target string
		want   bool
	}{
		{name: "auto source", source: lingomux.AutoLanguage, target: "en", want: true},
		{name: "canonical pair", source: "en", target: "zh-CN", want: true},
		{name: "traditional Chinese preserved", source: "zh-TW", target: "en", want: true},
		{name: "Brazilian Portuguese deliberate mapping", source: "en", target: "pt-BR", want: true},
		{name: "auto target", source: "en", target: lingomux.AutoLanguage, want: false},
		{name: "unlisted region is not stripped", source: "en-US", target: "fr", want: false},
		{name: "unknown source", source: "xx", target: "fr", want: false},
		{name: "unknown target", source: "en", target: "xx", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := provider.Supports(test.source, test.target); got != test.want {
				t.Errorf("Supports(%q, %q) = %t, want %t", test.source, test.target, got, test.want)
			}
		})
	}
}

func TestTranslateSendsGoogleBasicV2RequestAndParsesSuccess(t *testing.T) {
	const apiKey = "query-key-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got, want := request.URL.Path, "/language/translate/v2"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if got := request.URL.Query().Get("key"); got != apiKey {
			t.Errorf("key query = %q, want configured key", got)
		}
		if request.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", request.Method)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if got := payload["q"]; got != "Hello & goodbye" {
			t.Errorf("q = %#v, want input text", got)
		}
		if got := payload["target"]; got != "zh-TW" {
			t.Errorf("target = %#v, want zh-TW", got)
		}
		if got := payload["format"]; got != "text" {
			t.Errorf("format = %#v, want text", got)
		}
		if _, exists := payload["source"]; exists {
			t.Errorf("source was present for auto source: %#v", payload["source"])
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"data":{"translations":[{"translatedText":"你好 &amp;amp; 再見","detectedSourceLanguage":"en"}]}}`)
	}))
	defer server.Close()

	provider, err := New(Config{APIKey: apiKey, BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := provider.Translate(context.Background(), lingomux.Request{
		Text:           "Hello & goodbye",
		SourceLanguage: lingomux.AutoLanguage,
		TargetLanguage: "zh-TW",
	})
	if err != nil {
		t.Fatalf("Translate() error = %v", err)
	}
	if got, want := result.Text, "你好 &amp; 再見"; got != want {
		t.Errorf("text = %q, want %q (decode HTML entities exactly once)", got, want)
	}
	if got, want := result.SourceLanguage, "en"; got != want {
		t.Errorf("source = %q, want %q", got, want)
	}
}

func TestTranslateMapsExplicitLanguagesWithoutRegionGuessing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if got := payload["source"]; got != "zh-CN" {
			t.Errorf("source = %#v, want zh-CN", got)
		}
		if got := payload["target"]; got != "pt" {
			t.Errorf("target = %#v, want explicit Google pt mapping for pt-BR", got)
		}
		_, _ = io.WriteString(writer, `{"data":{"translations":[{"translatedText":"olá"}]}}`)
	}))
	defer server.Close()

	provider, err := New(Config{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := provider.Translate(context.Background(), lingomux.Request{
		Text: "你好", SourceLanguage: "zh-CN", TargetLanguage: "pt-BR",
	})
	if err != nil {
		t.Fatalf("Translate() error = %v", err)
	}
	if result.SourceLanguage != "zh-CN" {
		t.Errorf("source = %q, want explicit request source zh-CN", result.SourceLanguage)
	}
}

func TestTranslateMapsHTTPFailures(t *testing.T) {
	tests := []struct {
		status    int
		kind      lingomux.ErrorKind
		retryable bool
	}{
		{status: http.StatusBadRequest, kind: lingomux.ErrorInvalidRequest, retryable: false},
		{status: http.StatusUnauthorized, kind: lingomux.ErrorAuthentication, retryable: false},
		{status: http.StatusForbidden, kind: lingomux.ErrorAuthentication, retryable: false},
		{status: http.StatusTooManyRequests, kind: lingomux.ErrorRateLimited, retryable: true},
		{status: http.StatusInternalServerError, kind: lingomux.ErrorUnavailable, retryable: true},
		{status: http.StatusServiceUnavailable, kind: lingomux.ErrorUnavailable, retryable: true},
		{status: http.StatusTeapot, kind: lingomux.ErrorProviderFailure, retryable: false},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			const rawBody = "raw-provider-response-secret"
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, rawBody)
			}))
			defer server.Close()
			provider, err := New(Config{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = provider.Translate(context.Background(), validRequest())
			assertProviderError(t, err, test.kind, test.status, test.retryable)
			if strings.Contains(err.Error(), rawBody) {
				t.Errorf("error leaked response body: %q", err)
			}
		})
	}
}

func TestTranslateDoesNotFollowRedirects(t *testing.T) {
	const (
		apiKey     = "api-key-secret"
		sourceText = "source-body-secret"
	)
	tests := []struct {
		name   string
		status int
	}{
		{name: "302 found", status: http.StatusFound},
		{name: "307 temporary redirect", status: http.StatusTemporaryRedirect},
		{name: "308 permanent redirect", status: http.StatusPermanentRedirect},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var sourceRequests atomic.Int64
			var destinationRequests atomic.Int64
			var callerRedirectChecks atomic.Int64

			destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				destinationRequests.Add(1)
				if got := request.URL.Query().Get("key"); got != "" {
					t.Errorf("redirect forwarded API key %q", got)
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Errorf("read redirected body: %v", err)
				}
				if strings.Contains(string(body), sourceText) {
					t.Errorf("redirect replayed source text: %q", body)
				}
				_, _ = io.WriteString(writer, `{"data":{"translations":[{"translatedText":"redirected"}]}}`)
			}))
			defer destination.Close()

			source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				sourceRequests.Add(1)
				if got := request.URL.Query().Get("key"); got != apiKey {
					t.Errorf("source request key = %q, want configured key", got)
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Errorf("read source body: %v", err)
				}
				if !strings.Contains(string(body), sourceText) {
					t.Errorf("source request body = %q, want source text", body)
				}
				writer.Header().Set("Location", destination.URL+"/leak?key="+request.URL.Query().Get("key"))
				writer.WriteHeader(test.status)
			}))
			defer source.Close()

			callerClient := source.Client()
			callerClient.CheckRedirect = func(*http.Request, []*http.Request) error {
				callerRedirectChecks.Add(1)
				return nil
			}
			provider, err := New(Config{APIKey: apiKey, BaseURL: source.URL, HTTPClient: callerClient})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = provider.Translate(context.Background(), lingomux.Request{
				Text:           sourceText,
				SourceLanguage: lingomux.AutoLanguage,
				TargetLanguage: "fr",
			})
			if got := sourceRequests.Load(); got != 1 {
				t.Errorf("source requests = %d, want 1", got)
			}
			if got := destinationRequests.Load(); got != 0 {
				t.Errorf("destination requests = %d, want 0", got)
			}
			if got := callerRedirectChecks.Load(); got != 0 {
				t.Errorf("caller CheckRedirect calls = %d, want 0", got)
			}
			assertProviderError(t, err, lingomux.ErrorProviderFailure, test.status, false)
			for _, secret := range []string{apiKey, sourceText, "redirected"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error leaked %q: %q", secret, err)
				}
			}
		})
	}
}

func TestTranslatePrioritizesHTTPStatusOverResponseBodyFailures(t *testing.T) {
	readFailure := errors.New("response-reader-secret")
	tests := []struct {
		name      string
		status    int
		body      func() io.ReadCloser
		kind      lingomux.ErrorKind
		retryable bool
	}{
		{
			name:      "400 with oversized body",
			status:    http.StatusBadRequest,
			body:      oversizedResponseBody,
			kind:      lingomux.ErrorInvalidRequest,
			retryable: false,
		},
		{
			name:      "401 with failing body reader",
			status:    http.StatusUnauthorized,
			body:      func() io.ReadCloser { return &failingReadCloser{err: readFailure} },
			kind:      lingomux.ErrorAuthentication,
			retryable: false,
		},
		{
			name:      "403 with oversized body",
			status:    http.StatusForbidden,
			body:      oversizedResponseBody,
			kind:      lingomux.ErrorAuthentication,
			retryable: false,
		},
		{
			name:      "429 with failing body reader",
			status:    http.StatusTooManyRequests,
			body:      func() io.ReadCloser { return &failingReadCloser{err: readFailure} },
			kind:      lingomux.ErrorRateLimited,
			retryable: true,
		},
		{
			name:      "500 with oversized body",
			status:    http.StatusInternalServerError,
			body:      oversizedResponseBody,
			kind:      lingomux.ErrorUnavailable,
			retryable: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider, err := New(Config{
				APIKey:  "api-secret",
				BaseURL: "https://google.test",
				HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: test.status,
						Header:     make(http.Header),
						Body:       test.body(),
					}, nil
				})},
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = provider.Translate(context.Background(), validRequest())
			assertProviderError(t, err, test.kind, test.status, test.retryable)
			for _, secret := range []string{"api-secret", "source-secret", "response-reader-secret", "oversized-response-secret"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error leaked %q: %q", secret, err)
				}
			}
		})
	}
}

func TestTranslatePrioritizesContextFailureOverHTTPStatus(t *testing.T) {
	provider, err := New(Config{
		APIKey:  "key",
		BaseURL: "https://google.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     make(http.Header),
				Body:       &failingReadCloser{err: context.Canceled},
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = provider.Translate(context.Background(), validRequest())
	assertProviderError(t, err, lingomux.ErrorTimeout, http.StatusUnauthorized, true)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error does not retain context cancellation: %v", err)
	}
}

func TestTranslateRejectsMalformedAndEmptySuccessResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"data":`},
		{name: "empty translations", body: `{"data":{"translations":[]}}`},
		{name: "empty text", body: `{"data":{"translations":[{"translatedText":""}]}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			provider, err := New(Config{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = provider.Translate(context.Background(), validRequest())
			assertProviderError(t, err, lingomux.ErrorProviderFailure, http.StatusOK, true)
			if strings.Contains(err.Error(), test.body) {
				t.Errorf("error leaked response body: %q", err)
			}
		})
	}
}

func TestTranslateMapsTransportErrorsAndCancellation(t *testing.T) {
	transportCause := errors.New("transport failed with api-secret and source-secret")
	provider, err := New(Config{
		APIKey:  "api-secret",
		BaseURL: "https://google.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, transportCause
		})},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = provider.Translate(context.Background(), validRequest())
	assertProviderError(t, err, lingomux.ErrorUnavailable, 0, true)
	if !errors.Is(err, transportCause) {
		t.Errorf("error does not retain transport cause: %v", err)
	}
	for _, secret := range []string{"api-secret", "source-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaked %q: %q", secret, err)
		}
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = provider.Translate(canceled, validRequest())
	assertProviderError(t, err, lingomux.ErrorTimeout, 0, true)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error does not retain context cancellation: %v", err)
	}
}

func validRequest() lingomux.Request {
	return lingomux.Request{
		Text:           "source-secret",
		SourceLanguage: lingomux.AutoLanguage,
		TargetLanguage: "fr",
	}
}

func assertProviderError(t *testing.T, err error, kind lingomux.ErrorKind, status int, retryable bool) {
	t.Helper()
	var typed *lingomux.Error
	if !errors.As(err, &typed) {
		t.Fatalf("error type = %T, want *lingomux.Error", err)
	}
	if typed.Kind != kind || typed.Provider != "google" || typed.StatusCode != status || typed.Retryable != retryable {
		t.Errorf("error = %#v, want kind=%s provider=google status=%d retryable=%t", typed, kind, status, retryable)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type failingReadCloser struct {
	err error
}

func (body *failingReadCloser) Read([]byte) (int, error) {
	return 0, body.err
}

func (*failingReadCloser) Close() error {
	return nil
}

func oversizedResponseBody() io.ReadCloser {
	return io.NopCloser(strings.NewReader(strings.Repeat("oversized-response-secret", 1+(1<<20)/len("oversized-response-secret"))))
}

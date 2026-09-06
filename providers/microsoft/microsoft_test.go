package microsoft

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
	"github.com/Sakuragi27/lingomux/internal/httpjson"
)

func TestNewValidatesConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{name: "missing API key", config: Config{}},
		{name: "whitespace API key", config: Config{APIKey: " \t"}},
		{name: "relative endpoint", config: Config{APIKey: "key", Endpoint: "/relative"}},
		{name: "unsupported endpoint scheme", config: Config{APIKey: "key", Endpoint: "ftp://example.test"}},
		{name: "endpoint query", config: Config{APIKey: "key", Endpoint: "https://example.test?x=1"}},
		{name: "endpoint fragment", config: Config{APIKey: "key", Endpoint: "https://example.test#fragment"}},
		{name: "endpoint credentials", config: Config{APIKey: "key", Endpoint: "https://user@example.test"}},
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
	if got := provider.Name(); got != "microsoft" {
		t.Errorf("Name() = %q, want microsoft", got)
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
		{name: "simplified Chinese", source: "en", target: "zh-CN", want: true},
		{name: "traditional Chinese", source: "zh-TW", target: "en", want: true},
		{name: "generic Portuguese", source: "en", target: "pt", want: true},
		{name: "Brazilian Portuguese", source: "en", target: "pt-BR", want: true},
		{name: "European Portuguese", source: "pt-PT", target: "en", want: true},
		{name: "common Hindi", source: "hi", target: "ar", want: true},
		{name: "Vietnamese", source: "en", target: "vi", want: true},
		{name: "Thai", source: "th", target: "en", want: true},
		{name: "Indonesian", source: "en", target: "id", want: true},
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

func TestTranslateSendsAzureV3RequestAndParsesFirstTranslation(t *testing.T) {
	const (
		apiKey = "subscription-key-secret"
		region = "eastasia"
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got, want := request.Method, http.MethodPost; got != want {
			t.Errorf("method = %q, want %q", got, want)
		}
		if got, want := request.URL.Path, "/translate"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		query := request.URL.Query()
		if got, want := query.Get("api-version"), "3.0"; got != want {
			t.Errorf("api-version = %q, want %q", got, want)
		}
		if got, want := query.Get("to"), "zh-Hant"; got != want {
			t.Errorf("to = %q, want %q", got, want)
		}
		if _, exists := query["from"]; exists {
			t.Errorf("from was present for auto source: %q", query.Get("from"))
		}
		if got := request.Header.Get("Ocp-Apim-Subscription-Key"); got != apiKey {
			t.Errorf("subscription key header = %q, want configured key", got)
		}
		if got := request.Header.Get("Ocp-Apim-Subscription-Region"); got != region {
			t.Errorf("subscription region header = %q, want configured region", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got, want := string(body), `[{"text":"Hello"}]`; got != want {
			t.Errorf("body = %q, want exactly %q", got, want)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `[{"detectedLanguage":{"language":"en","score":1},"translations":[{"text":"你好","to":"zh-Hant"},{"text":"ignored","to":"fr"}]},{"translations":[{"text":"ignored item","to":"de"}]}]`)
	}))
	defer server.Close()

	provider, err := New(Config{
		APIKey: apiKey, Region: region, Endpoint: server.URL + "/", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := provider.Translate(context.Background(), lingomux.Request{
		Text: "Hello", SourceLanguage: lingomux.AutoLanguage, TargetLanguage: "zh-TW",
	})
	if err != nil {
		t.Fatalf("Translate() error = %v", err)
	}
	if got, want := result.Text, "你好"; got != want {
		t.Errorf("text = %q, want first translation %q", got, want)
	}
	if got, want := result.SourceLanguage, "en"; got != want {
		t.Errorf("source = %q, want detected %q", got, want)
	}
}

func TestTranslateMapsExplicitChineseAndPortugueseVariants(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		target      string
		wantFrom    string
		wantTo      string
		translation string
	}{
		{name: "simplified Chinese to European Portuguese", source: "zh-CN", target: "pt-PT", wantFrom: "zh-Hans", wantTo: "pt-pt", translation: "olá"},
		{name: "traditional Chinese to Brazilian Portuguese", source: "zh-TW", target: "pt-BR", wantFrom: "zh-Hant", wantTo: "pt", translation: "olá"},
		{name: "generic Portuguese stays explicit", source: "pt", target: "en", wantFrom: "pt", wantTo: "en", translation: "hello"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				query := request.URL.Query()
				if got := query.Get("from"); got != test.wantFrom {
					t.Errorf("from = %q, want %q", got, test.wantFrom)
				}
				if got := query.Get("to"); got != test.wantTo {
					t.Errorf("to = %q, want %q", got, test.wantTo)
				}
				if got := request.Header.Get("Ocp-Apim-Subscription-Region"); got != "" {
					t.Errorf("optional region header = %q, want absent", got)
				}
				_ = json.NewEncoder(writer).Encode([]map[string]any{{
					"translations": []map[string]string{{"text": test.translation, "to": test.wantTo}},
				}})
			}))
			defer server.Close()

			provider, err := New(Config{APIKey: "key", Endpoint: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			result, err := provider.Translate(context.Background(), lingomux.Request{
				Text: "source", SourceLanguage: test.source, TargetLanguage: test.target,
			})
			if err != nil {
				t.Fatalf("Translate() error = %v", err)
			}
			if result.SourceLanguage != test.source {
				t.Errorf("source = %q, want explicit source %q", result.SourceLanguage, test.source)
			}
		})
	}
}

func TestTranslateMapsDetectedVendorLanguagesBackToCanonical(t *testing.T) {
	tests := []struct {
		name     string
		detected string
		want     string
	}{
		{name: "simplified Chinese", detected: "zh-Hans", want: "zh-CN"},
		{name: "traditional Chinese", detected: "zh-Hant", want: "zh-TW"},
		{name: "Brazilian Portuguese", detected: "pt", want: "pt-BR"},
		{name: "European Portuguese", detected: "pt-pt", want: "pt-PT"},
		{name: "unknown vendor code", detected: "xx-vendor", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(writer).Encode([]map[string]any{{
					"detectedLanguage": map[string]any{"language": test.detected, "score": 1},
					"translations":     []map[string]string{{"text": "translated", "to": "en"}},
				}})
			}))
			defer server.Close()

			provider, err := New(Config{APIKey: "key", Endpoint: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			result, err := provider.Translate(context.Background(), lingomux.Request{
				Text: "source", SourceLanguage: lingomux.AutoLanguage, TargetLanguage: "en",
			})
			if err != nil {
				t.Fatalf("Translate() error = %v", err)
			}
			if result.SourceLanguage != test.want {
				t.Errorf("source = %q, want %q", result.SourceLanguage, test.want)
			}
		})
	}
}

func TestTranslateMapsHTTPFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		kind      lingomux.ErrorKind
		retryable bool
	}{
		{name: "400 bad request", status: http.StatusBadRequest, kind: lingomux.ErrorInvalidRequest},
		{name: "401 unauthorized", status: http.StatusUnauthorized, kind: lingomux.ErrorAuthentication},
		{name: "403 forbidden", status: http.StatusForbidden, kind: lingomux.ErrorAuthentication},
		{name: "408 timeout", status: http.StatusRequestTimeout, kind: lingomux.ErrorTimeout, retryable: true},
		{name: "429 throttled", status: http.StatusTooManyRequests, kind: lingomux.ErrorRateLimited, retryable: true},
		{name: "500 internal server error", status: http.StatusInternalServerError, kind: lingomux.ErrorUnavailable, retryable: true},
		{name: "503 unavailable", status: http.StatusServiceUnavailable, kind: lingomux.ErrorUnavailable, retryable: true},
		{name: "418 unknown client error", status: http.StatusTeapot, kind: lingomux.ErrorProviderFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const rawBody = "raw-provider-response-secret"
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, rawBody)
			}))
			defer server.Close()
			provider, err := New(Config{APIKey: "api-secret", Endpoint: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = provider.Translate(context.Background(), validRequest())
			assertProviderError(t, err, test.kind, test.status, test.retryable)
			for _, secret := range []string{"api-secret", "source-secret", rawBody} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error leaked %q: %q", secret, err)
				}
			}
		})
	}
}

func TestTranslateDoesNotFollowRedirects(t *testing.T) {
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
			const (
				apiKey        = "redirect-api-key-secret"
				redirectBody  = "redirect-body-secret"
				locationQuery = "redirect-location-secret"
			)
			var sourceCalls atomic.Int32
			var destinationCalls atomic.Int32
			var callerRedirectPolicyCalls atomic.Int32
			var destinationKey atomic.Value
			var destinationBody atomic.Value
			destinationKey.Store("")
			destinationBody.Store("")

			destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				destinationCalls.Add(1)
				destinationKey.Store(request.Header.Get("Ocp-Apim-Subscription-Key"))
				body, _ := io.ReadAll(request.Body)
				destinationBody.Store(string(body))
				_, _ = io.WriteString(writer, `[{"translations":[{"text":"redirected","to":"fr"}]}]`)
			}))
			defer destination.Close()

			source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				sourceCalls.Add(1)
				writer.Header().Set("Location", destination.URL+"/leak?value="+locationQuery)
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, redirectBody)
			}))
			defer source.Close()

			client := source.Client()
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				callerRedirectPolicyCalls.Add(1)
				return nil
			}
			provider, err := New(Config{APIKey: apiKey, Endpoint: source.URL, HTTPClient: client})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = provider.Translate(context.Background(), validRequest())
			assertProviderError(t, err, lingomux.ErrorProviderFailure, test.status, false)
			if got := sourceCalls.Load(); got != 1 {
				t.Errorf("source request count = %d, want 1", got)
			}
			if got := destinationCalls.Load(); got != 0 {
				t.Errorf("redirect destination request count = %d, want 0", got)
			}
			if got := callerRedirectPolicyCalls.Load(); got != 0 {
				t.Errorf("caller redirect policy calls = %d, want provider policy override", got)
			}
			if got := destinationKey.Load().(string); got != "" {
				t.Errorf("redirect destination received subscription key %q", got)
			}
			if got := destinationBody.Load().(string); got != "" {
				t.Errorf("redirect destination received request body %q", got)
			}
			for _, secret := range []string{apiKey, "source-secret", redirectBody, locationQuery} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error leaked %q: %q", secret, err)
				}
			}
		})
	}
}

func TestTranslatePrioritizesKnownHTTPStatusOverResponseBodyFailures(t *testing.T) {
	readFailure := errors.New("response-reader-secret")
	tests := []struct {
		name      string
		status    int
		body      func() io.ReadCloser
		kind      lingomux.ErrorKind
		retryable bool
	}{
		{name: "400 with oversized body", status: http.StatusBadRequest, body: oversizedResponseBody, kind: lingomux.ErrorInvalidRequest},
		{name: "401 with failing body", status: http.StatusUnauthorized, body: func() io.ReadCloser { return &failingReadCloser{err: readFailure} }, kind: lingomux.ErrorAuthentication},
		{name: "408 with oversized body", status: http.StatusRequestTimeout, body: oversizedResponseBody, kind: lingomux.ErrorTimeout, retryable: true},
		{name: "429 with failing body", status: http.StatusTooManyRequests, body: func() io.ReadCloser { return &failingReadCloser{err: readFailure} }, kind: lingomux.ErrorRateLimited, retryable: true},
		{name: "500 with oversized body", status: http.StatusInternalServerError, body: oversizedResponseBody, kind: lingomux.ErrorUnavailable, retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider, err := New(Config{
				APIKey: "api-secret", Endpoint: "https://microsoft.test",
				HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: test.body()}, nil
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
		APIKey: "key", Endpoint: "https://microsoft.test",
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

func TestTranslatePrioritizesContextCanceledDuringSuccessfulBodyReadOverHTTPStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	provider, err := New(Config{
		APIKey: "key", Endpoint: "https://microsoft.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Header:     make(http.Header),
				Body: &cancelingReadCloser{
					data:   []byte(`[]`),
					cancel: cancel,
				},
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = provider.Translate(ctx, validRequest())
	assertProviderError(t, err, lingomux.ErrorTimeout, http.StatusForbidden, true)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error does not retain context cancellation: %v", err)
	}
}

func TestTranslateRejectsMalformedEmptyAndOversizedSuccessResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `[{"translations":`},
		{name: "empty response items", body: `[]`},
		{name: "empty translations", body: `[{"translations":[]}]`},
		{name: "empty translated text", body: `[{"translations":[{"text":"","to":"fr"}]}]`},
		{name: "oversized response", body: strings.Repeat("oversized-response-secret", 1+httpjson.MaxResponseBytes/len("oversized-response-secret"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			provider, err := New(Config{APIKey: "key", Endpoint: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = provider.Translate(context.Background(), validRequest())
			assertProviderError(t, err, lingomux.ErrorProviderFailure, http.StatusOK, true)
			if strings.Contains(err.Error(), test.body) || strings.Contains(err.Error(), "oversized-response-secret") {
				t.Errorf("error leaked response body: %q", err)
			}
		})
	}
}

func TestTranslateMapsTransportErrorsAndCancellation(t *testing.T) {
	transportCause := errors.New("transport failed with api-secret and source-secret")
	provider, err := New(Config{
		APIKey: "api-secret", Endpoint: "https://microsoft.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, transportCause
		})},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = provider.Translate(context.Background(), validRequest())
	assertProviderError(t, err, lingomux.ErrorProviderFailure, 0, true)
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

func TestTranslateRejectsNilContextAndUnsupportedLanguagesWithoutTraffic(t *testing.T) {
	var calls int
	provider, err := New(Config{
		APIKey: "key", Endpoint: "https://microsoft.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("unexpected traffic")
		})},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = provider.Translate(nil, validRequest())
	assertProviderError(t, err, lingomux.ErrorInvalidRequest, 0, false)
	request := validRequest()
	request.TargetLanguage = "xx"
	_, err = provider.Translate(context.Background(), request)
	assertProviderError(t, err, lingomux.ErrorUnsupportedLanguage, 0, false)
	if calls != 0 {
		t.Errorf("transport calls = %d, want 0", calls)
	}
}

func validRequest() lingomux.Request {
	return lingomux.Request{
		Text: "source-secret", SourceLanguage: lingomux.AutoLanguage, TargetLanguage: "fr",
	}
}

func assertProviderError(t *testing.T, err error, kind lingomux.ErrorKind, status int, retryable bool) {
	t.Helper()
	var typed *lingomux.Error
	if !errors.As(err, &typed) {
		t.Fatalf("error type = %T, want *lingomux.Error", err)
	}
	if typed.Kind != kind || typed.Provider != "microsoft" || typed.StatusCode != status || typed.Retryable != retryable {
		t.Errorf("error = %#v, want kind=%s provider=microsoft status=%d retryable=%t", typed, kind, status, retryable)
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

type cancelingReadCloser struct {
	data   []byte
	cancel context.CancelFunc
	read   bool
}

func (body *cancelingReadCloser) Read(destination []byte) (int, error) {
	if body.read {
		return 0, io.EOF
	}
	body.read = true
	written := copy(destination, body.data)
	body.cancel()
	return written, io.EOF
}

func (*cancelingReadCloser) Close() error {
	return nil
}

func oversizedResponseBody() io.ReadCloser {
	return io.NopCloser(strings.NewReader(strings.Repeat("oversized-response-secret", 1+httpjson.MaxResponseBytes/len("oversized-response-secret"))))
}

package deepl

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
		{name: "relative base URL", config: Config{APIKey: "key", BaseURL: "/relative"}},
		{name: "unsupported URL scheme", config: Config{APIKey: "key", BaseURL: "ftp://example.test"}},
		{name: "URL query", config: Config{APIKey: "key", BaseURL: "https://example.test?x=1"}},
		{name: "URL fragment", config: Config{APIKey: "key", BaseURL: "https://example.test#fragment"}},
		{name: "URL credentials", config: Config{APIKey: "key", BaseURL: "https://user@example.test"}},
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
	if got := provider.Name(); got != "deepl" {
		t.Errorf("Name() = %q, want deepl", got)
	}
}

func TestTranslateUsesPaidEndpointByDefault(t *testing.T) {
	var gotURL string
	provider, err := New(Config{
		APIKey: "key",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			gotURL = request.URL.String()
			return jsonResponse(http.StatusOK, `{"translations":[{"detected_source_language":"EN","text":"bonjour"}]}`), nil
		})},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := provider.Translate(context.Background(), validRequest()); err != nil {
		t.Fatalf("Translate() error = %v", err)
	}
	if want := "https://api.deepl.com/v2/translate"; gotURL != want {
		t.Errorf("request URL = %q, want %q", gotURL, want)
	}
}

func TestTranslateSendsDeepLRequestToConfiguredFreeEndpointAndParsesSuccess(t *testing.T) {
	const apiKey = "deepl-api-key-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got, want := request.Method, http.MethodPost; got != want {
			t.Errorf("method = %q, want %q", got, want)
		}
		if got, want := request.URL.Path, "/gateway/v2/translate"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if got, want := request.Header.Get("Authorization"), "DeepL-Auth-Key "+apiKey; got != want {
			t.Errorf("Authorization = %q, want DeepL auth scheme and configured key", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		texts, ok := payload["text"].([]any)
		if !ok || len(texts) != 1 || texts[0] != "Hello" {
			t.Errorf("text = %#v, want one-element array containing input", payload["text"])
		}
		if got := payload["target_lang"]; got != "ZH-HANT" {
			t.Errorf("target_lang = %#v, want ZH-HANT", got)
		}
		if _, exists := payload["source_lang"]; exists {
			t.Errorf("source_lang was present for auto source: %#v", payload["source_lang"])
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"translations":[{"detected_source_language":"EN","text":"你好"},{"detected_source_language":"DE","text":"ignored"}]}`)
	}))
	defer server.Close()

	provider, err := New(Config{APIKey: apiKey, BaseURL: server.URL + "/gateway/", HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := provider.Translate(context.Background(), lingomux.Request{
		Text: "Hello", SourceLanguage: lingomux.AutoLanguage, TargetLanguage: "zh-TW",
	})
	if err != nil {
		t.Fatalf("Translate() error = %v", err)
	}
	if result.Text != "你好" || result.SourceLanguage != "en" {
		t.Errorf("result = %#v, want first text and canonical detected source", result)
	}
}

func TestSupportsUsesSeparateExplicitSourceAndTargetMaps(t *testing.T) {
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
		{name: "simplified Chinese target", source: "en", target: "zh-CN", want: true},
		{name: "traditional Chinese target", source: "en", target: "zh-TW", want: true},
		{name: "generic Portuguese target", source: "en", target: "pt", want: true},
		{name: "Brazilian Portuguese target", source: "en", target: "pt-BR", want: true},
		{name: "Chinese source uses source map", source: "zh-TW", target: "en", want: true},
		{name: "Portuguese source uses source map", source: "pt-BR", target: "en", want: true},
		{name: "auto target", source: "en", target: lingomux.AutoLanguage, want: false},
		{name: "documented Arabic target", source: "en", target: "ar", want: true},
		{name: "Hindi", source: "hi", target: "en", want: true},
		{name: "unlisted source region is not stripped", source: "en-US", target: "fr", want: false},
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

func TestTranslateMapsDirectionSpecificChineseAndPortugueseVariants(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		target     string
		wantSource string
		wantTarget string
	}{
		{name: "traditional Chinese to Brazilian Portuguese", source: "zh-TW", target: "pt-BR", wantSource: "ZH", wantTarget: "PT-BR"},
		{name: "simplified Chinese to generic Portuguese", source: "zh-CN", target: "pt", wantSource: "ZH", wantTarget: "PT"},
		{name: "Brazilian Portuguese to traditional Chinese", source: "pt-BR", target: "zh-TW", wantSource: "PT", wantTarget: "ZH-HANT"},
		{name: "generic Portuguese to simplified Chinese", source: "pt", target: "zh-CN", wantSource: "PT", wantTarget: "ZH-HANS"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatalf("decode payload: %v", err)
				}
				if got := payload["source_lang"]; got != test.wantSource {
					t.Errorf("source_lang = %#v, want %q", got, test.wantSource)
				}
				if got := payload["target_lang"]; got != test.wantTarget {
					t.Errorf("target_lang = %#v, want %q", got, test.wantTarget)
				}
				_, _ = io.WriteString(writer, `{"translations":[{"detected_source_language":"ZH","text":"translated"}]}`)
			}))
			defer server.Close()

			provider, err := New(Config{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client()})
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

func TestTranslateMapsDetectedDeepLLanguagesToCanonical(t *testing.T) {
	tests := []struct {
		name     string
		detected string
		want     string
	}{
		{name: "English", detected: "EN", want: "en"},
		{name: "Chinese defaults to simplified for reusable detection", detected: "ZH", want: "zh-CN"},
		{name: "Portuguese", detected: "PT", want: "pt"},
		{name: "unknown vendor code", detected: "XX-VENDOR", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(writer).Encode(map[string]any{"translations": []map[string]string{{
					"detected_source_language": test.detected, "text": "translated",
				}}})
			}))
			defer server.Close()
			provider, err := New(Config{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			result, err := provider.Translate(context.Background(), validRequest())
			if err != nil {
				t.Fatalf("Translate() error = %v", err)
			}
			if result.SourceLanguage != test.want {
				t.Errorf("source = %q, want %q", result.SourceLanguage, test.want)
			}
		})
	}
}

func TestDetectedLanguagesCanBeReusedAsTargets(t *testing.T) {
	provider, err := New(Config{APIKey: "key"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for vendorCode, canonical := range deepLToCanonical {
		if !provider.Supports(lingomux.AutoLanguage, canonical) {
			t.Errorf("detected language %q maps to %q, which cannot be reused as a target", vendorCode, canonical)
		}
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
		{name: "403 forbidden", status: http.StatusForbidden, kind: lingomux.ErrorAuthentication},
		{name: "404 invalid endpoint", status: http.StatusNotFound, kind: lingomux.ErrorProviderFailure},
		{name: "408 timeout", status: http.StatusRequestTimeout, kind: lingomux.ErrorTimeout, retryable: true},
		{name: "429 throttled", status: http.StatusTooManyRequests, kind: lingomux.ErrorRateLimited, retryable: true},
		{name: "456 quota exhausted", status: 456, kind: lingomux.ErrorRateLimited, retryable: true},
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
			provider, err := New(Config{APIKey: "api-secret", BaseURL: server.URL, HTTPClient: server.Client()})
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
				apiKey       = "redirect-api-key-secret"
				sourceText   = "redirect-source-secret"
				redirectBody = "redirect-body-secret"
			)
			var sourceCalls atomic.Int32
			var destinationCalls atomic.Int32
			var callerRedirectPolicyCalls atomic.Int32

			destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				destinationCalls.Add(1)
				if got := request.Header.Get("Authorization"); got != "" {
					t.Errorf("redirect destination received Authorization %q", got)
				}
				body, _ := io.ReadAll(request.Body)
				if strings.Contains(string(body), sourceText) {
					t.Errorf("redirect destination received source text %q", body)
				}
				_, _ = io.WriteString(writer, `{"translations":[{"text":"redirected"}]}`)
			}))
			defer destination.Close()

			source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				sourceCalls.Add(1)
				writer.Header().Set("Location", destination.URL+"/leak")
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, redirectBody)
			}))
			defer source.Close()

			client := source.Client()
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				callerRedirectPolicyCalls.Add(1)
				return nil
			}
			provider, err := New(Config{APIKey: apiKey, BaseURL: source.URL, HTTPClient: client})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = provider.Translate(context.Background(), lingomux.Request{
				Text: sourceText, SourceLanguage: lingomux.AutoLanguage, TargetLanguage: "fr",
			})
			assertProviderError(t, err, lingomux.ErrorProviderFailure, test.status, false)
			if got := sourceCalls.Load(); got != 1 {
				t.Errorf("source request count = %d, want 1", got)
			}
			if got := destinationCalls.Load(); got != 0 {
				t.Errorf("redirect destination request count = %d, want 0", got)
			}
			if got := callerRedirectPolicyCalls.Load(); got != 0 {
				t.Errorf("caller redirect policy calls = %d, want provider override", got)
			}
			for _, secret := range []string{apiKey, sourceText, redirectBody} {
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
		{name: "403 with failing body", status: http.StatusForbidden, body: func() io.ReadCloser { return &failingReadCloser{err: readFailure} }, kind: lingomux.ErrorAuthentication},
		{name: "404 with oversized body", status: http.StatusNotFound, body: oversizedResponseBody, kind: lingomux.ErrorProviderFailure},
		{name: "408 with failing body", status: http.StatusRequestTimeout, body: func() io.ReadCloser { return &failingReadCloser{err: readFailure} }, kind: lingomux.ErrorTimeout, retryable: true},
		{name: "429 with oversized body", status: http.StatusTooManyRequests, body: oversizedResponseBody, kind: lingomux.ErrorRateLimited, retryable: true},
		{name: "456 with failing body", status: 456, body: func() io.ReadCloser { return &failingReadCloser{err: readFailure} }, kind: lingomux.ErrorRateLimited, retryable: true},
		{name: "500 with oversized body", status: http.StatusInternalServerError, body: oversizedResponseBody, kind: lingomux.ErrorUnavailable, retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider, err := New(Config{
				APIKey: "api-secret", BaseURL: "https://deepl.test",
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
		APIKey: "key", BaseURL: "https://deepl.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Header:     make(http.Header),
				Body:       &failingReadCloser{err: context.Canceled},
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = provider.Translate(context.Background(), validRequest())
	assertProviderError(t, err, lingomux.ErrorTimeout, http.StatusForbidden, true)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error does not retain context cancellation: %v", err)
	}
}

func TestTranslatePrioritizesContextCanceledDuringSuccessfulBodyReadOverHTTPStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	provider, err := New(Config{
		APIKey: "key", BaseURL: "https://deepl.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Header:     make(http.Header),
				Body: &cancelingReadCloser{
					data:   []byte("{\"translations\":[]}"),
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
		{name: "malformed JSON", body: `{"translations":`},
		{name: "empty translations", body: `{"translations":[]}`},
		{name: "empty translated text", body: `{"translations":[{"detected_source_language":"EN","text":""}]}`},
		{name: "oversized response", body: strings.Repeat("oversized-response-secret", 1+httpjson.MaxResponseBytes/len("oversized-response-secret"))},
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
			if strings.Contains(err.Error(), test.body) || strings.Contains(err.Error(), "oversized-response-secret") {
				t.Errorf("error leaked response body: %q", err)
			}
		})
	}
}

func TestTranslateMapsTransportErrorsAndCancellation(t *testing.T) {
	transportCause := errors.New("transport failed with api-secret and source-secret")
	provider, err := New(Config{
		APIKey: "api-secret", BaseURL: "https://deepl.test",
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
		APIKey: "key", BaseURL: "https://deepl.test",
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
	if typed.Kind != kind || typed.Provider != "deepl" || typed.StatusCode != status || typed.Retryable != retryable {
		t.Errorf("error = %#v, want kind=%s provider=deepl status=%d retryable=%t", typed, kind, status, retryable)
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
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

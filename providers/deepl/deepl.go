// Package deepl implements the DeepL text translation REST API.
package deepl

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Sakuragi27/lingomux"
	"github.com/Sakuragi27/lingomux/internal/httpjson"
)

const (
	providerName   = "deepl"
	defaultBaseURL = "https://api.deepl.com"
	translatePath  = "/v2/translate"
)

var (
	errInvalidConfiguration = errors.New("deepl: invalid configuration")
	errUnsupportedLanguage  = errors.New("deepl: unsupported language pair")
	errEmptyTranslation     = errors.New("deepl: empty translation response")
)

// Config configures the DeepL translation provider.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider translates text through DeepL.
type Provider struct {
	baseURL    *url.URL
	httpClient *http.Client
}

// New constructs a DeepL provider. BaseURL defaults to the paid endpoint;
// DeepL Free users should configure https://api-free.deepl.com.
func New(config Config) (*Provider, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, invalidConfigurationError()
	}
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, invalidConfigurationError()
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""

	client := config.HTTPClient
	if client == nil {
		client = httpjson.NewClient()
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	clientCopy.Transport = &authorizationTransport{base: transport, apiKey: config.APIKey}

	return &Provider{baseURL: parsed, httpClient: &clientCopy}, nil
}

// Name returns the stable provider identifier.
func (*Provider) Name() string {
	return providerName
}

// Supports reports whether the source and target have explicit DeepL mappings.
// Auto-detection is supported only for the source language.
func (*Provider) Supports(sourceLanguage, targetLanguage string) bool {
	if _, ok := canonicalToDeepLTarget[targetLanguage]; !ok {
		return false
	}
	if sourceLanguage == lingomux.AutoLanguage {
		return true
	}
	_, ok := canonicalToDeepLSource[sourceLanguage]
	return ok
}

// Translate submits one text segment to DeepL.
func (provider *Provider) Translate(ctx context.Context, request lingomux.Request) (lingomux.ProviderResult, error) {
	if ctx == nil {
		return lingomux.ProviderResult{}, lingomux.NewProviderError(
			lingomux.ErrorInvalidRequest, providerName, 0, false, errInvalidConfiguration,
		)
	}
	if !provider.Supports(request.SourceLanguage, request.TargetLanguage) {
		return lingomux.ProviderResult{}, lingomux.NewProviderError(
			lingomux.ErrorUnsupportedLanguage, providerName, 0, false, errUnsupportedLanguage,
		)
	}

	endpoint := *provider.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + translatePath
	payload := translateRequest{
		Text:       []string{request.Text},
		TargetLang: canonicalToDeepLTarget[request.TargetLanguage],
	}
	if request.SourceLanguage != lingomux.AutoLanguage {
		payload.SourceLang = canonicalToDeepLSource[request.SourceLanguage]
	}

	response, err := httpjson.Do(ctx, provider.httpClient, http.MethodPost, endpoint.String(), payload)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			cause := err
			if ctx.Err() != nil {
				cause = ctx.Err()
			}
			return lingomux.ProviderResult{}, lingomux.NewProviderError(
				lingomux.ErrorTimeout, providerName, response.StatusCode, true, cause,
			)
		}
		if response.StatusCode != 0 &&
			(response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices) {
			return lingomux.ProviderResult{}, mapHTTPError(response.StatusCode)
		}
		return lingomux.ProviderResult{}, lingomux.NewProviderError(
			lingomux.ErrorProviderFailure, providerName, response.StatusCode, true, err,
		)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return lingomux.ProviderResult{}, mapHTTPError(response.StatusCode)
	}

	var decoded translateResponse
	if err := response.DecodeJSON(&decoded); err != nil {
		return lingomux.ProviderResult{}, lingomux.NewProviderError(
			lingomux.ErrorProviderFailure, providerName, response.StatusCode, true, err,
		)
	}
	if len(decoded.Translations) == 0 || decoded.Translations[0].Text == "" {
		return lingomux.ProviderResult{}, lingomux.NewProviderError(
			lingomux.ErrorProviderFailure, providerName, response.StatusCode, true, errEmptyTranslation,
		)
	}

	translation := decoded.Translations[0]
	sourceLanguage := request.SourceLanguage
	if request.SourceLanguage == lingomux.AutoLanguage {
		sourceLanguage = deepLToCanonical[translation.DetectedSourceLanguage]
	}
	return lingomux.ProviderResult{Text: translation.Text, SourceLanguage: sourceLanguage}, nil
}

type translateRequest struct {
	Text       []string `json:"text"`
	TargetLang string   `json:"target_lang"`
	SourceLang string   `json:"source_lang,omitempty"`
}

type translateResponse struct {
	Translations []struct {
		DetectedSourceLanguage string `json:"detected_source_language"`
		Text                   string `json:"text"`
	} `json:"translations"`
}

type authorizationTransport struct {
	base   http.RoundTripper
	apiKey string
}

func (transport *authorizationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	requestCopy := request.Clone(request.Context())
	requestCopy.Header = request.Header.Clone()
	requestCopy.Header.Set("Authorization", "DeepL-Auth-Key "+transport.apiKey)
	return transport.base.RoundTrip(requestCopy)
}

func invalidConfigurationError() *lingomux.Error {
	return lingomux.NewProviderError(
		lingomux.ErrorInvalidRequest, providerName, 0, false, errInvalidConfiguration,
	)
}

func mapHTTPError(statusCode int) *lingomux.Error {
	kind := lingomux.ErrorProviderFailure
	retryable := false
	switch {
	case statusCode == http.StatusBadRequest:
		kind = lingomux.ErrorInvalidRequest
	case statusCode == http.StatusForbidden:
		kind = lingomux.ErrorAuthentication
	case statusCode == http.StatusRequestTimeout:
		kind = lingomux.ErrorTimeout
		retryable = true
	case statusCode == http.StatusTooManyRequests || statusCode == 456:
		kind = lingomux.ErrorRateLimited
		retryable = true
	case statusCode >= http.StatusInternalServerError:
		kind = lingomux.ErrorUnavailable
		retryable = true
	}
	return lingomux.NewProviderError(kind, providerName, statusCode, retryable, nil)
}

var _ lingomux.Provider = (*Provider)(nil)
var _ http.RoundTripper = (*authorizationTransport)(nil)

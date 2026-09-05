// Package microsoft implements Microsoft Azure AI Translator Text API v3.
package microsoft

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
	providerName    = "microsoft"
	defaultEndpoint = "https://api.cognitive.microsofttranslator.com"
	translatePath   = "/translate"
)

var (
	errInvalidConfiguration = errors.New("microsoft: invalid configuration")
	errUnsupportedLanguage  = errors.New("microsoft: unsupported language pair")
	errEmptyTranslation     = errors.New("microsoft: empty translation response")
)

// Config configures Microsoft Azure AI Translator.
type Config struct {
	APIKey     string
	Region     string
	Endpoint   string
	HTTPClient *http.Client
}

// Provider translates text through Microsoft Azure AI Translator.
type Provider struct {
	endpoint   *url.URL
	httpClient *http.Client
}

// New constructs a Microsoft Azure AI Translator provider.
func New(config Config) (*Provider, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, invalidConfigurationError()
	}
	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	parsed, err := url.Parse(endpoint)
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
	clientCopy.Transport = &subscriptionTransport{
		base: transport, apiKey: config.APIKey, region: config.Region,
	}

	return &Provider{endpoint: parsed, httpClient: &clientCopy}, nil
}

// Name returns the stable provider identifier.
func (*Provider) Name() string {
	return providerName
}

// Supports reports whether both canonical language tags have explicit Azure
// mappings. Auto-detection is supported only for the source language.
func (*Provider) Supports(sourceLanguage, targetLanguage string) bool {
	if _, ok := canonicalToMicrosoft[targetLanguage]; !ok {
		return false
	}
	if sourceLanguage == lingomux.AutoLanguage {
		return true
	}
	_, ok := canonicalToMicrosoft[sourceLanguage]
	return ok
}

// Translate submits one text segment to Microsoft Azure AI Translator.
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

	endpoint := *provider.endpoint
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + translatePath
	query := endpoint.Query()
	query.Set("api-version", "3.0")
	query.Set("to", canonicalToMicrosoft[request.TargetLanguage])
	if request.SourceLanguage != lingomux.AutoLanguage {
		query.Set("from", canonicalToMicrosoft[request.SourceLanguage])
	}
	endpoint.RawQuery = query.Encode()

	payload := []translateRequest{{Text: request.Text}}
	response, err := httpjson.Do(ctx, provider.httpClient, http.MethodPost, endpoint.String(), payload)
	if ctx.Err() != nil {
		return lingomux.ProviderResult{}, lingomux.NewProviderError(
			lingomux.ErrorTimeout, providerName, response.StatusCode, true, ctx.Err(),
		)
	}
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

	var decoded []translateResponse
	if err := response.DecodeJSON(&decoded); err != nil {
		return lingomux.ProviderResult{}, lingomux.NewProviderError(
			lingomux.ErrorProviderFailure, providerName, response.StatusCode, true, err,
		)
	}
	if len(decoded) == 0 || len(decoded[0].Translations) == 0 || decoded[0].Translations[0].Text == "" {
		return lingomux.ProviderResult{}, lingomux.NewProviderError(
			lingomux.ErrorProviderFailure, providerName, response.StatusCode, true, errEmptyTranslation,
		)
	}

	sourceLanguage := request.SourceLanguage
	if request.SourceLanguage == lingomux.AutoLanguage {
		sourceLanguage = microsoftToCanonical[decoded[0].DetectedLanguage.Language]
	}
	return lingomux.ProviderResult{
		Text:           decoded[0].Translations[0].Text,
		SourceLanguage: sourceLanguage,
	}, nil
}

type translateRequest struct {
	Text string `json:"text"`
}

type translateResponse struct {
	DetectedLanguage struct {
		Language string `json:"language"`
	} `json:"detectedLanguage"`
	Translations []struct {
		Text string `json:"text"`
	} `json:"translations"`
}

type subscriptionTransport struct {
	base   http.RoundTripper
	apiKey string
	region string
}

func (transport *subscriptionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	requestCopy := request.Clone(request.Context())
	requestCopy.Header = request.Header.Clone()
	requestCopy.Header.Set("Ocp-Apim-Subscription-Key", transport.apiKey)
	if transport.region != "" {
		requestCopy.Header.Set("Ocp-Apim-Subscription-Region", transport.region)
	}
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
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		kind = lingomux.ErrorAuthentication
	case statusCode == http.StatusRequestTimeout:
		kind = lingomux.ErrorTimeout
		retryable = true
	case statusCode == http.StatusTooManyRequests:
		kind = lingomux.ErrorRateLimited
		retryable = true
	case statusCode >= http.StatusInternalServerError:
		kind = lingomux.ErrorUnavailable
		retryable = true
	}
	return lingomux.NewProviderError(kind, providerName, statusCode, retryable, nil)
}

var _ lingomux.Provider = (*Provider)(nil)
var _ http.RoundTripper = (*subscriptionTransport)(nil)

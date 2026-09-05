// Package openai implements translation through the OpenAI Responses REST API.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Sakuragi27/lingomux"
	"github.com/Sakuragi27/lingomux/internal/httpjson"
)

const providerName = "openai"

var (
	errInvalidConfiguration = errors.New("openai: invalid configuration")
	errUnsupportedLanguage  = errors.New("openai: unsupported language pair")
	errEmptyTranslation     = errors.New("openai: empty translation response")
	errInvalidResponse      = errors.New("openai: invalid translation response")
)

// Config configures the OpenAI translation provider. Model must be selected
// explicitly by the caller.
type Config struct {
	APIKey string
	Model  string
	// BaseURL is the API root, normally ending in /v1; /responses is appended.
	BaseURL         string
	MaxOutputTokens int
	HTTPClient      *http.Client
}

// Provider translates text through the Responses API.
type Provider struct {
	baseURL         *url.URL
	httpClient      *http.Client
	model           string
	maxOutputTokens int
}

// New constructs a provider. BaseURL defaults to https://api.openai.com/v1;
// MaxOutputTokens defaults to 2048 when zero.
func New(config Config) (*Provider, error) {
	if strings.TrimSpace(config.APIKey) == "" || strings.TrimSpace(config.Model) == "" || config.MaxOutputTokens < 0 {
		return nil, invalidConfigurationError()
	}
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, invalidConfigurationError()
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	client := config.HTTPClient
	if client == nil {
		client = httpjson.NewClient()
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	clientCopy.Transport = &authorizationTransport{base: transport, apiKey: config.APIKey}
	tokens := config.MaxOutputTokens
	if tokens == 0 {
		tokens = 2048
	}
	return &Provider{baseURL: parsed, httpClient: &clientCopy, model: config.Model, maxOutputTokens: tokens}, nil
}

// Name returns the stable provider identifier.
func (*Provider) Name() string { return providerName }

// Supports accepts only explicitly mapped canonical tags, with auto permitted
// only as a source language.
func (*Provider) Supports(sourceLanguage, targetLanguage string) bool {
	if _, ok := languageDisplayNames[targetLanguage]; !ok {
		return false
	}
	if sourceLanguage == lingomux.AutoLanguage {
		return true
	}
	_, ok := languageDisplayNames[sourceLanguage]
	return ok
}

// Translate submits one input string without retaining conversation state.
func (provider *Provider) Translate(ctx context.Context, request lingomux.Request) (lingomux.ProviderResult, error) {
	if ctx == nil {
		return lingomux.ProviderResult{}, invalidConfigurationError()
	}
	if !provider.Supports(request.SourceLanguage, request.TargetLanguage) {
		return lingomux.ProviderResult{}, lingomux.NewProviderError(lingomux.ErrorUnsupportedLanguage, providerName, 0, false, errUnsupportedLanguage)
	}
	endpoint := *provider.baseURL
	endpoint.Path += "/responses"
	payload := responsesRequest{
		Model: provider.model, Store: false, MaxOutputTokens: provider.maxOutputTokens,
		Instructions: translationInstructions(request.SourceLanguage, request.TargetLanguage), Input: request.Text,
	}
	response, err := httpjson.Do(ctx, provider.httpClient, http.MethodPost, endpoint.String(), payload)
	if ctx.Err() != nil {
		return lingomux.ProviderResult{}, lingomux.NewProviderError(lingomux.ErrorTimeout, providerName, response.StatusCode, true, ctx.Err())
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return lingomux.ProviderResult{}, lingomux.NewProviderError(lingomux.ErrorTimeout, providerName, response.StatusCode, true, err)
	}
	if response.StatusCode != 0 && (response.StatusCode < 200 || response.StatusCode >= 300) {
		return lingomux.ProviderResult{}, mapHTTPError(response.StatusCode)
	}
	if err != nil {
		return lingomux.ProviderResult{}, responseFailure(response.StatusCode, err)
	}
	text, err := translatedText(response)
	if err != nil {
		return lingomux.ProviderResult{}, responseFailure(response.StatusCode, err)
	}
	source := request.SourceLanguage
	if source == lingomux.AutoLanguage {
		source = ""
	}
	return lingomux.ProviderResult{Text: text, SourceLanguage: source}, nil
}

type responsesRequest struct {
	Model           string `json:"model"`
	Store           bool   `json:"store"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	Instructions    string `json:"instructions"`
	Input           string `json:"input"`
}

func translationInstructions(source, target string) string {
	instruction := "Detect the source language and translate the input into " + languageDisplayNames[target] + ". "
	if source != lingomux.AutoLanguage {
		instruction = "Translate the input from " + languageDisplayNames[source] + " into " + languageDisplayNames[target] + ". "
	}
	return instruction + "Return only the translated text. Preserve URLs, @mentions, emoji, whitespace, line breaks, and formatting."
}

func translatedText(response httpjson.Response) (string, error) {
	var decoded struct {
		Status            string `json:"status"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Output []struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		} `json:"output"`
	}
	if err := response.DecodeJSON(&decoded); err != nil {
		return "", err
	}
	if decoded.Status != "completed" {
		return "", errInvalidResponse
	}
	var text strings.Builder
	for _, output := range decoded.Output {
		if output.Type != "message" {
			continue
		}
		var content []struct {
			Type string          `json:"type"`
			Text json.RawMessage `json:"text"`
		}
		if err := json.Unmarshal(output.Content, &content); err != nil {
			return "", err
		}
		if content == nil {
			return "", errInvalidResponse
		}
		for _, item := range content {
			if item.Type != "output_text" {
				continue
			}
			var fragment *string
			if err := json.Unmarshal(item.Text, &fragment); err != nil {
				return "", err
			}
			if fragment == nil {
				return "", errInvalidResponse
			}
			text.WriteString(*fragment)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", errEmptyTranslation
	}
	return text.String(), nil
}

type authorizationTransport struct {
	base   http.RoundTripper
	apiKey string
}

func (transport *authorizationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	requestCopy := request.Clone(request.Context())
	requestCopy.Header = request.Header.Clone()
	requestCopy.Header.Set("Authorization", "Bearer "+transport.apiKey)
	return transport.base.RoundTrip(requestCopy)
}

func invalidConfigurationError() *lingomux.Error {
	return lingomux.NewProviderError(lingomux.ErrorInvalidRequest, providerName, 0, false, errInvalidConfiguration)
}

func responseFailure(status int, cause error) *lingomux.Error {
	return lingomux.NewProviderError(lingomux.ErrorProviderFailure, providerName, status, true, cause)
}

func mapHTTPError(status int) *lingomux.Error {
	kind, retryable := lingomux.ErrorProviderFailure, false
	switch {
	case status == http.StatusBadRequest:
		kind = lingomux.ErrorInvalidRequest
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = lingomux.ErrorAuthentication
	case status == http.StatusRequestTimeout:
		kind, retryable = lingomux.ErrorTimeout, true
	case status == http.StatusTooManyRequests:
		kind, retryable = lingomux.ErrorRateLimited, true
	case status >= http.StatusInternalServerError:
		kind, retryable = lingomux.ErrorUnavailable, true
	}
	return lingomux.NewProviderError(kind, providerName, status, retryable, nil)
}

var _ lingomux.Provider = (*Provider)(nil)
var _ http.RoundTripper = (*authorizationTransport)(nil)

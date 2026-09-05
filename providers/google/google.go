// Package google implements Google Cloud Translation Basic v2.
package google

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/Sakuragi27/lingomux"
	"github.com/Sakuragi27/lingomux/internal/httpjson"
)

const (
	providerName   = "google"
	defaultBaseURL = "https://translation.googleapis.com"
	translatePath  = "/language/translate/v2"
)

var (
	errInvalidConfiguration = errors.New("google: invalid configuration")
	errUnsupportedLanguage  = errors.New("google: unsupported language pair")
	errEmptyTranslation     = errors.New("google: empty translation response")
)

// Config configures the Google Cloud Translation provider.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider translates text through Google Cloud Translation Basic v2.
type Provider struct {
	apiKey     string
	baseURL    *url.URL
	httpClient *http.Client
}

// New constructs a Google provider.
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
	providerClient := *client
	providerClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Provider{apiKey: config.APIKey, baseURL: parsed, httpClient: &providerClient}, nil
}

// Name returns the stable provider identifier.
func (*Provider) Name() string {
	return providerName
}

// Supports reports whether both canonical language tags have explicit Google
// mappings. Auto-detection is supported only for the source language.
func (*Provider) Supports(sourceLanguage, targetLanguage string) bool {
	if _, ok := canonicalToGoogle[targetLanguage]; !ok {
		return false
	}
	if sourceLanguage == lingomux.AutoLanguage {
		return true
	}
	_, ok := canonicalToGoogle[sourceLanguage]
	return ok
}

// Translate submits one text segment to Google Cloud Translation Basic v2.
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

	payload := translateRequest{
		Query:  request.Text,
		Target: canonicalToGoogle[request.TargetLanguage],
		Format: "text",
	}
	if request.SourceLanguage != lingomux.AutoLanguage {
		payload.Source = canonicalToGoogle[request.SourceLanguage]
	}
	endpoint := *provider.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + translatePath
	query := endpoint.Query()
	query.Set("key", provider.apiKey)
	endpoint.RawQuery = query.Encode()

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
		if response.StatusCode != 0 {
			return lingomux.ProviderResult{}, lingomux.NewProviderError(
				lingomux.ErrorProviderFailure, providerName, response.StatusCode, true, err,
			)
		}
		return lingomux.ProviderResult{}, lingomux.NewProviderError(
			lingomux.ErrorUnavailable, providerName, 0, true, err,
		)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode == http.StatusForbidden && isQuotaError(response) {
			return lingomux.ProviderResult{}, lingomux.NewProviderError(
				lingomux.ErrorRateLimited, providerName, response.StatusCode, true, nil,
			)
		}
		return lingomux.ProviderResult{}, mapHTTPError(response.StatusCode)
	}

	var decoded translateResponse
	if err := response.DecodeJSON(&decoded); err != nil {
		return lingomux.ProviderResult{}, lingomux.NewProviderError(
			lingomux.ErrorProviderFailure, providerName, response.StatusCode, true, err,
		)
	}
	if len(decoded.Data.Translations) == 0 || decoded.Data.Translations[0].TranslatedText == "" {
		return lingomux.ProviderResult{}, lingomux.NewProviderError(
			lingomux.ErrorProviderFailure, providerName, response.StatusCode, true, errEmptyTranslation,
		)
	}

	translation := decoded.Data.Translations[0]
	sourceLanguage := request.SourceLanguage
	if mapped, ok := googleToCanonical[translation.DetectedSourceLanguage]; ok {
		sourceLanguage = mapped
	} else if sourceLanguage == lingomux.AutoLanguage {
		sourceLanguage = ""
	}
	return lingomux.ProviderResult{
		Text:           html.UnescapeString(translation.TranslatedText),
		SourceLanguage: sourceLanguage,
	}, nil
}

type translateRequest struct {
	Query  string `json:"q"`
	Target string `json:"target"`
	Format string `json:"format"`
	Source string `json:"source,omitempty"`
}

type translateResponse struct {
	Data struct {
		Translations []struct {
			TranslatedText         string `json:"translatedText"`
			DetectedSourceLanguage string `json:"detectedSourceLanguage,omitempty"`
		} `json:"translations"`
	} `json:"data"`
}

func invalidConfigurationError() *lingomux.Error {
	return lingomux.NewProviderError(
		lingomux.ErrorInvalidRequest, providerName, 0, false, errInvalidConfiguration,
	)
}

// isQuotaError accepts only structured quota indicators from the bounded body.
func isQuotaError(response httpjson.Response) bool {
	var decoded struct {
		Error struct {
			Status string `json:"status"`
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if err := response.DecodeJSON(&decoded); err != nil {
		return false
	}
	if decoded.Error.Status == "RESOURCE_EXHAUSTED" {
		return true
	}
	for _, detail := range decoded.Error.Errors {
		switch detail.Reason {
		case "dailyLimitExceeded", "userRateLimitExceeded", "rateLimitExceeded", "quotaExceeded":
			return true
		}
	}
	return false
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

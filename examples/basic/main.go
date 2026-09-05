package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/Sakuragi27/lingomux"
	"github.com/Sakuragi27/lingomux/providers/deepl"
	"github.com/Sakuragi27/lingomux/providers/google"
	"github.com/Sakuragi27/lingomux/providers/microsoft"
	"github.com/Sakuragi27/lingomux/providers/openai"
)

func main() {
	googleProvider, err := google.New(google.Config{
		APIKey: os.Getenv("GOOGLE_TRANSLATE_API_KEY"),
	})
	if err != nil {
		log.Fatal(err)
	}

	microsoftProvider, err := microsoft.New(microsoft.Config{
		APIKey: os.Getenv("AZURE_TRANSLATOR_API_KEY"),
		Region: os.Getenv("AZURE_TRANSLATOR_REGION"),
	})
	if err != nil {
		log.Fatal(err)
	}

	deepLProvider, err := deepl.New(deepl.Config{
		APIKey:  os.Getenv("DEEPL_API_KEY"),
		BaseURL: os.Getenv("DEEPL_BASE_URL"),
	})
	if err != nil {
		log.Fatal(err)
	}

	openAIProvider, err := openai.New(openai.Config{
		APIKey: os.Getenv("OPENAI_API_KEY"),
		Model:  os.Getenv("OPENAI_MODEL"),
	})
	if err != nil {
		log.Fatal(err)
	}

	client, err := lingomux.New(lingomux.WithProviders(
		googleProvider,
		microsoftProvider,
		deepLProvider,
		openAIProvider,
	))
	if err != nil {
		log.Fatal(err)
	}

	result, err := client.Translate(context.Background(), lingomux.Request{
		Text:           "Hello from LingoMux!",
		SourceLanguage: lingomux.AutoLanguage,
		TargetLanguage: "zh-CN",
		Provider:       lingomux.AutoProvider,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("[%s] %s\n", result.Provider, result.Text)
}

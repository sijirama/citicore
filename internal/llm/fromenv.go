package llm

import (
    "fmt"
    "os"
    "strings"
)

func FromEnv() (Client, error) {
    provider := strings.ToLower(os.Getenv("LLM_PROVIDER"))
    key := os.Getenv("OPENAI_API_KEY")
    model := os.Getenv("OPENAI_MODEL")
    baseURL := ""
    if provider == "openrouter" {
        key = os.Getenv("OPENROUTER_API_KEY")
        baseURL = "https://openrouter.ai/api/v1"
    }
    if provider != "" && provider != "openai" && provider != "openrouter" {
        return nil, fmt.Errorf("LLM_PROVIDER %q is not supported by the chat adapter", provider)
    }
    if key == "" || model == "" { return nil, fmt.Errorf("chat model key or OPENAI_MODEL is missing") }
    return NewOpenAICompatible(key, model, baseURL), nil
}
